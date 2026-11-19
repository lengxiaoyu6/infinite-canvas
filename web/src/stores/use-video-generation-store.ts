import { create } from "zustand";
import localforage from "localforage";
import { nanoid } from "nanoid";

import i18n from "@/i18n";
import { clampVideoSeconds, computeVideoSize, inferVideoRatio, parseVideoResolution } from "@/lib/media-size";
import { createVideoGenerationTask, waitForVideoGenerationTask, storeGeneratedVideo, type VideoGenerationTask } from "@/services/api/video";
import { deleteStoredMedia, resolveMediaUrl } from "@/services/file-storage";
import { ensureImagePreview, resolveImageUrl } from "@/services/image-storage";
import { boolConfig, useConfigStore, type AiConfig } from "@/stores/use-config-store";
import { useWorkbenchAgentStore } from "@/stores/use-workbench-agent-store";
import type { ReferenceImage } from "@/types/image";

export type GeneratedVideo = {
    id: string;
    url: string;
    storageKey: string;
    durationMs: number;
    width: number;
    height: number;
    bytes: number;
    mimeType: string;
};

type GenerationLogConfig = Pick<AiConfig, "model" | "videoModel" | "size" | "vquality" | "videoSeconds" | "videoGenerateAudio" | "videoWatermark" | "videoMode">;

export type VideoGenerationLog = {
    id: string;
    createdAt: number;
    title: string;
    prompt: string;
    time: string;
    model: string;
    config: GenerationLogConfig;
    references: ReferenceImage[];
    durationMs: number;
    size: string;
    resolution: string;
    seconds: string;
    status: "pending" | "success" | "failed";
    task?: VideoGenerationTask;
    video?: GeneratedVideo;
    error?: string;
};

type StateUpdate<T> = T | ((value: T) => T);
type VideoGenerationStore = {
    prompt: string;
    references: ReferenceImage[];
    logs: VideoGenerationLog[];
    activeLogId: string | null;
    setPrompt: (value: StateUpdate<string>) => void;
    setReferences: (value: StateUpdate<ReferenceImage[]>) => void;
    loadLogs: () => Promise<void>;
    createSession: () => void;
    selectLog: (log: VideoGenerationLog) => void;
    generate: (snapshot: { text: string; config: AiConfig; references: ReferenceImage[] }, agentTaskId?: string) => Promise<VideoGenerationLog>;
    deleteLogs: (ids: string[]) => Promise<void>;
};

const logStore = localforage.createInstance({ name: "infinite-canvas", storeName: "video_generation_logs" });
const activeTasks = new Map<string, Promise<VideoGenerationLog>>();
let loading: Promise<void> | undefined;
let writes: Promise<unknown> = Promise.resolve();

export const useVideoGenerationStore = create<VideoGenerationStore>((set, get) => ({
    prompt: "",
    references: [],
    logs: [],
    activeLogId: null,
    setPrompt: (value) => set((state) => ({ prompt: typeof value === "function" ? value(state.prompt) : value })),
    setReferences: (value) => set((state) => ({ references: typeof value === "function" ? value(state.references) : value })),
    loadLogs: () => {
        loading ??= (async () => {
            const values: VideoGenerationLog[] = [];
            await logStore.iterate<VideoGenerationLog, void>((value) => { values.push(value); });
            const logs = await Promise.all(values.map(async (log) => ({
                ...log,
                video: log.video ? { ...log.video, url: await resolveMediaUrl(log.video.storageKey, log.video.url) } : undefined,
                references: await Promise.all(log.references.map(async (image) => {
                    void ensureImagePreview(image.storageKey);
                    return { ...image, dataUrl: await resolveImageUrl(image.storageKey, image.dataUrl) };
                })),
            })));
            const sorted = logs.sort((a, b) => b.createdAt - a.createdAt);
            const pending = sorted.find((log) => log.status === "pending");
            set((state) => ({
                logs: sorted,
                ...(!state.activeLogId && !state.prompt && !state.references.length && pending
                    ? { activeLogId: pending.id, prompt: pending.prompt, references: pending.references }
                    : {}),
            }));
            for (const log of logs) {
                if (log.status === "pending" && log.task) void resumeTask(log).catch(() => undefined);
            }
        })().catch((error) => { loading = undefined; throw error; });
        return loading;
    },
    createSession: () => set({ prompt: "", references: [], activeLogId: null }),
    selectLog: (log) => set({ activeLogId: log.id, prompt: log.prompt, references: log.references }),
    generate: async (snapshot, agentTaskId) => {
        await get().loadLogs();
        if (get().logs.some((log) => log.status === "pending")) throw new Error(i18n.t("videoWorkbench.busy"));
        const config = buildVideoConfig(snapshot.config, snapshot.config.model || snapshot.config.videoModel);
        const { model, videoModel, size, vquality, videoSeconds, videoGenerateAudio, videoWatermark, videoMode } = config;
        const createdAt = Date.now();
        const log: VideoGenerationLog = {
            id: nanoid(), createdAt, title: snapshot.text.slice(0, 12), prompt: snapshot.text,
            time: new Date(createdAt).toLocaleString(i18n.resolvedLanguage, { hour12: false }),
            model, config: { model, videoModel, size, vquality, videoSeconds, videoGenerateAudio, videoWatermark, videoMode },
            references: snapshot.references, durationMs: 0, size, resolution: vquality, seconds: videoSeconds, status: "pending",
        };
        set((state) => ({ logs: [log, ...state.logs], activeLogId: log.id }));
        if (agentTaskId) useWorkbenchAgentStore.getState().updateTask(agentTaskId, { status: "running", error: undefined });
        try {
            log.task = await createVideoGenerationTask(config, snapshot.text, snapshot.references);
            updateLog({ ...log });
            await persistLog(log);
        } catch (error) {
            return finishTask(log, undefined, error, agentTaskId);
        }
        return resumeTask(log, config, agentTaskId);
    },
    deleteLogs: async (ids) => {
        const removed = get().logs.filter((log) => ids.includes(log.id) && log.status !== "pending");
        const removedIds = new Set(removed.map((log) => log.id));
        set((state) => ({
            logs: state.logs.filter((log) => !removedIds.has(log.id)),
            activeLogId: state.activeLogId && removedIds.has(state.activeLogId) ? null : state.activeLogId,
        }));
        await writes.catch(() => undefined);
        await Promise.all([
            deleteStoredMedia(removed.flatMap((log) => log.video?.storageKey ? [log.video.storageKey] : [])),
            ...removed.map((log) => logStore.removeItem(log.id)),
        ]);
    },
}));

function updateLog(log: VideoGenerationLog) {
    useVideoGenerationStore.setState((state) => ({ logs: state.logs.map((item) => item.id === log.id ? log : item) }));
}

function persistLog(log: VideoGenerationLog) {
    const value = {
        ...log,
        references: log.references.map((image) => ({ ...image, dataUrl: image.storageKey ? "" : image.dataUrl })),
        video: log.video?.storageKey ? { ...log.video, url: "" } : log.video,
    };
    writes = writes.catch(() => undefined).then(() => logStore.setItem(log.id, value));
    return writes;
}

function resumeTask(log: VideoGenerationLog, configOverride?: AiConfig, agentTaskId?: string) {
    const active = activeTasks.get(log.id);
    if (active) return active;
    const task = (async () => {
        let video: GeneratedVideo | undefined;
        let error: unknown;
        try {
            const { config: currentConfig, isAiConfigReady } = useConfigStore.getState();
            const config = configOverride || buildVideoConfig({ ...currentConfig, channelMode: "local", ...log.config }, log.task!.model || log.model);
            if (!isAiConfigReady(config, config.model)) throw new Error(i18n.t("workbench.configFirst"));
            // Use the existing polling interval and attempt limit, with one owner per task.
            const stored = await storeGeneratedVideo(await waitForVideoGenerationTask(config, log.task!));
            video = {
                id: nanoid(), url: stored.url, storageKey: stored.storageKey, durationMs: Date.now() - log.createdAt,
                width: stored.width || 1280, height: stored.height || 720, bytes: stored.bytes, mimeType: stored.mimeType,
            };
        } catch (cause) {
            error = cause;
        }
        return finishTask(log, video, error, agentTaskId);
    })().finally(() => activeTasks.delete(log.id));
    activeTasks.set(log.id, task);
    return task;
}

async function finishTask(log: VideoGenerationLog, video?: GeneratedVideo, error?: unknown, agentTaskId?: string): Promise<VideoGenerationLog> {
    const next: VideoGenerationLog = {
        ...log, video, status: video ? "success" : "failed", durationMs: Date.now() - log.createdAt,
        error: video ? undefined : error instanceof Error ? error.message : i18n.t("workbench.generationFailed"),
    };
    // Publish completion before persistence; the selected record always reads this same state.
    updateLog(next);
    if (agentTaskId) useWorkbenchAgentStore.getState().updateTask(agentTaskId, {
        status: video ? "succeeded" : "failed", successCount: video ? 1 : 0, failCount: video ? 0 : 1, error: next.error,
    });
    await persistLog(next);
    return next;
}

export function buildVideoConfig(config: AiConfig, model: string): AiConfig {
    const ratio = inferVideoRatio(config.size);
    return {
        ...config, model, videoModel: model,
        size: config.size === "auto" || /^\d+x\d+$/.test(config.size) ? config.size : ratio === "auto" ? "auto" : computeVideoSize("720", ratio),
        videoSeconds: String(config.videoSeconds).trim() === "-1" ? "-1" : clampVideoSeconds(config.videoSeconds),
        vquality: parseVideoResolution(config.vquality),
        videoGenerateAudio: String(boolConfig(config.videoGenerateAudio, true)),
        videoWatermark: String(boolConfig(config.videoWatermark, false)),
        videoMode: config.videoMode === "reference" ? "reference" : "frames",
    };
}
