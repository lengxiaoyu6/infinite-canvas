import { create } from "zustand";
import localforage from "localforage";
import { nanoid } from "nanoid";

import i18n from "@/i18n";
import { requestEdit, requestGeneration } from "@/services/api/image";
import { deleteStoredImages, ensureImagePreview, resolveImageUrl, uploadImage } from "@/services/image-storage";
import { defaultConfig, type AiConfig } from "@/stores/use-config-store";
import { useWorkbenchAgentStore } from "@/stores/use-workbench-agent-store";
import type { ReferenceImage } from "@/types/image";

export type GeneratedImage = {
    id: string;
    dataUrl: string;
    storageKey?: string;
    durationMs: number;
    width: number;
    height: number;
    bytes: number;
    mimeType?: string;
};

type GenerationResult = {
    id: string;
    status: "pending" | "success" | "failed" | "unknown";
    image?: GeneratedImage;
    error?: string;
};

type GenerationLogConfig = Pick<AiConfig, "model" | "imageModel" | "quality" | "imageOutputFormat" | "imageFrameCount" | "size" | "count">;

export type GenerationLog = {
    id: string;
    createdAt: number;
    title: string;
    prompt: string;
    time: string;
    model: string;
    config: GenerationLogConfig;
    references: ReferenceImage[];
    durationMs: number;
    successCount: number;
    failCount: number;
    unknownCount: number;
    imageCount: number;
    size: string;
    quality: string;
    status: "pending" | "success" | "failed" | "unknown";
    images: GeneratedImage[];
    error?: string;
};

type GenerationSnapshot = { text: string; config: AiConfig; references: ReferenceImage[] };
type StateUpdate<T> = T | ((value: T) => T);

type ImageGenerationStore = {
    prompt: string;
    references: ReferenceImage[];
    logs: GenerationLog[];
    resultsByLogId: Record<string, GenerationResult[]>;
    activeLogId: string | null;
    startedAt: number;
    setPrompt: (value: StateUpdate<string>) => void;
    setReferences: (value: StateUpdate<ReferenceImage[]>) => void;
    loadLogs: () => Promise<void>;
    createSession: () => void;
    selectLog: (log: GenerationLog) => void;
    generate: (snapshot: GenerationSnapshot, count: number, agentTaskId?: string) => Promise<GenerationLog>;
    retryResult: (logId: string, index: number, config: AiConfig, confirmResubmit?: boolean) => Promise<GenerationLog>;
    deleteLogs: (ids: string[]) => Promise<void>;
};

const logStore = localforage.createInstance({ name: "infinite-canvas", storeName: "image_generation_logs" });
let loading: Promise<void> | undefined;
let writes: Promise<unknown> = Promise.resolve();

export const useImageGenerationStore = create<ImageGenerationStore>((set, get) => ({
    prompt: "",
    references: [],
    logs: [],
    resultsByLogId: {},
    activeLogId: null,
    startedAt: 0,
    setPrompt: (value) => set((state) => ({ prompt: typeof value === "function" ? value(state.prompt) : value })),
    setReferences: (value) => set((state) => ({ references: typeof value === "function" ? value(state.references) : value })),
    loadLogs: () => {
        loading ??= (async () => {
            const values: GenerationLog[] = [];
            await logStore.iterate<GenerationLog, void>((value) => { values.push(value); });
            const logs = await Promise.all(values.map(normalizeLog));
            for (const log of logs) {
                if (log.status !== "pending") continue;
                // Losing the response does not cancel the provider's request.
                log.unknownCount = log.imageCount - log.images.length - log.failCount;
                log.status = log.unknownCount ? "unknown" : log.images.length ? "success" : "failed";
                await persistLog(log);
            }
            set({
                logs: logs.sort((a, b) => b.createdAt - a.createdAt),
                resultsByLogId: Object.fromEntries(logs.map((log) => [log.id, [
                    ...log.images.map((image) => ({ id: image.id, status: "success" as const, image })),
                    ...Array.from({ length: log.failCount }, () => ({ id: nanoid(), status: "failed" as const, error: log.error || i18n.t("workbench.generationFailed") })),
                    ...Array.from({ length: log.unknownCount }, () => ({ id: nanoid(), status: "unknown" as const, error: i18n.t("imageWorkbench.interrupted") })),
                ]])),
            });
        })().catch((error) => { loading = undefined; throw error; });
        return loading;
    },
    createSession: () => set({ prompt: "", references: [], activeLogId: null }),
    selectLog: (log) => set({ activeLogId: log.id, prompt: log.prompt, references: log.references }),
    generate: async (snapshot, count, agentTaskId) => {
        await get().loadLogs();
        if (get().logs.some((log) => log.status === "pending")) throw new Error(i18n.t("imageWorkbench.busy"));
        const { model, imageModel, quality, imageOutputFormat, imageFrameCount, size } = snapshot.config;
        const now = Date.now();
        const log: GenerationLog = {
            id: nanoid(), createdAt: now, title: snapshot.text.slice(0, 12), prompt: snapshot.text,
            time: new Date(now).toLocaleString(i18n.resolvedLanguage, { hour12: false }), model,
            config: { model, imageModel, quality, imageOutputFormat, imageFrameCount, size, count: String(count) },
            references: snapshot.references, durationMs: 0, successCount: 0, failCount: 0, unknownCount: 0,
            imageCount: count, size, quality, status: "pending", images: [],
        };
        set((state) => ({
            logs: [log, ...state.logs], activeLogId: log.id, startedAt: now,
            resultsByLogId: { ...state.resultsByLogId, [log.id]: Array.from({ length: count }, () => ({ id: nanoid(), status: "pending" as const })) },
        }));
        return runBatch(log.id, Array.from({ length: count }, (_, index) => index), snapshot, agentTaskId);
    },
    retryResult: async (logId, index, config, confirmResubmit = false) => {
        if (get().logs.some((log) => log.status === "pending")) throw new Error(i18n.t("imageWorkbench.busy"));
        const log = get().logs.find((item) => item.id === logId);
        const result = get().resultsByLogId[logId]?.[index];
        if (!log || !result || (result.status !== "failed" && result.status !== "unknown")) throw new Error(i18n.t("imageWorkbench.invalidParams"));
        if (result.status === "unknown" && !confirmResubmit) throw new Error(i18n.t("imageWorkbench.resubmitWarning"));
        set({ startedAt: Date.now() });
        updateResult(logId, index, { status: "pending", image: undefined, error: undefined });
        return runBatch(logId, [index], { text: log.prompt, config: { ...config, ...log.config, model: log.model, count: "1" }, references: log.references });
    },
    deleteLogs: async (ids) => {
        const removed = get().logs.filter((log) => ids.includes(log.id) && log.status !== "pending");
        const removedIds = new Set(removed.map((log) => log.id));
        set((state) => ({
            logs: state.logs.filter((log) => !removedIds.has(log.id)),
            resultsByLogId: Object.fromEntries(Object.entries(state.resultsByLogId).filter(([id]) => !removedIds.has(id))),
            activeLogId: state.activeLogId && removedIds.has(state.activeLogId) ? null : state.activeLogId,
        }));
        await writes.catch(() => undefined);
        await Promise.all([
            deleteStoredImages(removed.flatMap((log) => log.images.flatMap((image) => image.storageKey ? [image.storageKey] : []))),
            ...removed.map((log) => logStore.removeItem(log.id)),
        ]);
    },
}));

function persistLog(log: GenerationLog) {
    const value = serializeLog(log);
    writes = writes.catch(() => undefined).then(() => logStore.setItem(log.id, value));
    return writes;
}

function updateResult(logId: string, index: number, patch: Partial<GenerationResult>) {
    useImageGenerationStore.setState((state) => {
        const results = state.resultsByLogId[logId].map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item);
        const images = results.flatMap((item) => item.status === "success" && item.image ? [item.image] : []);
        return {
            resultsByLogId: { ...state.resultsByLogId, [logId]: results },
            logs: state.logs.map<GenerationLog>((log) => log.id === logId ? {
                ...log, images, successCount: images.length, failCount: results.filter((item) => item.status === "failed").length,
                unknownCount: results.filter((item) => item.status === "unknown").length,
                status: "pending",
                durationMs: Date.now() - state.startedAt,
                error: results.find((item) => item.status === "failed")?.error,
            } : log),
        };
    });
}

async function runBatch(logId: string, indexes: number[], snapshot: GenerationSnapshot, agentTaskId?: string) {
    const currentLog = () => useImageGenerationStore.getState().logs.find((log) => log.id === logId)!;
    const updateAgentTask = useWorkbenchAgentStore.getState().updateTask;
    if (agentTaskId) updateAgentTask(agentTaskId, { status: "running", error: undefined });
    try {
        // Persist the pending record before sending requests; never store provider credentials.
        await persistLog(currentLog());
        await Promise.all(indexes.map(async (index) => {
            const startedAt = performance.now();
            try {
                const response = snapshot.references.length ? await requestEdit(snapshot.config, snapshot.text, snapshot.references) : await requestGeneration(snapshot.config, snapshot.text);
                const image = response[0];
                if (!image) throw new Error(i18n.t("imageWorkbench.missingResult"));
                const stored = await uploadImage(image.dataUrl);
                updateResult(logId, index, { status: "success", error: undefined, image: {
                    id: image.id, dataUrl: stored.url, storageKey: stored.storageKey, durationMs: performance.now() - startedAt,
                    width: stored.width, height: stored.height, bytes: stored.bytes, mimeType: stored.mimeType,
                } });
            } catch (error) {
                updateResult(logId, index, { status: "failed", error: error instanceof Error ? error.message : i18n.t("workbench.generationFailed") });
            }
            await persistLog(currentLog()).catch(() => undefined);
        }));
    } catch (error) {
        for (const index of indexes) updateResult(logId, index, { status: "failed", error: error instanceof Error ? error.message : i18n.t("workbench.generationFailed") });
    }
    const log: GenerationLog = { ...currentLog(), status: currentLog().unknownCount ? "unknown" : currentLog().successCount ? "success" : "failed" };
    useImageGenerationStore.setState((state) => ({ logs: state.logs.map((item) => item.id === logId ? log : item) }));
    if (agentTaskId) updateAgentTask(agentTaskId, { status: log.successCount ? "succeeded" : "failed", successCount: log.successCount, failCount: log.failCount, error: log.successCount ? undefined : log.error });
    await persistLog(log);
    return log;
}

async function normalizeLog(log: Partial<GenerationLog>): Promise<GenerationLog> {
    const references = await Promise.all(
        (log.references || []).map(async (item) => {
            void ensureImagePreview(item.storageKey);
            return { ...item, dataUrl: await resolveImageUrl(item.storageKey, item.dataUrl) };
        }),
    );
    const images = await Promise.all(
        (log.images || []).map(async (item) => {
            void ensureImagePreview(item.storageKey);
            return { ...item, dataUrl: await resolveImageUrl(item.storageKey, item.dataUrl) };
        }),
    );
    const config = normalizeLogConfig(log);
    return {
        id: log.id || nanoid(),
        createdAt: log.createdAt || Date.now(),
        title: log.title || log.model || i18n.t("workbench.untitled"),
        prompt: log.prompt || log.title || "",
        time: log.time || new Date().toLocaleString(i18n.resolvedLanguage, { hour12: false }),
        model: log.model || config.imageModel || "",
        config,
        references,
        durationMs: log.durationMs || 0,
        successCount: log.successCount ?? log.imageCount ?? 0,
        failCount: log.failCount || 0,
        unknownCount: log.unknownCount || 0,
        imageCount: log.imageCount || log.successCount || 0,
        size: log.size || config.size || "",
        quality: log.quality || config.quality || "",
        status: log.status || "success",
        error: log.error,
        images,
    };
}

function serializeLog(log: GenerationLog): GenerationLog {
    return {
        ...log,
        references: log.references.map((item) => ({ ...item, dataUrl: item.storageKey ? "" : item.dataUrl })),
        images: log.images.map((image) => ({ ...image, dataUrl: image.storageKey ? "" : image.dataUrl })),
    };
}

function normalizeLogConfig(log: Partial<GenerationLog>): GenerationLogConfig {
    return {
        model: log.config?.model || log.model || "",
        imageModel: log.config?.imageModel || log.model || "",
        quality: log.config?.quality || log.quality || "",
        imageOutputFormat: log.config?.imageOutputFormat || "png",
        imageFrameCount: log.config?.imageFrameCount || defaultConfig.imageFrameCount,
        size: log.config?.size || log.size || "",
        count: log.config?.count || String(log.imageCount || log.successCount || 1),
    };
}
