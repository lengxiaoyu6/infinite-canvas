import { beforeEach, expect, mock, test } from "bun:test";
import { spawnSync } from "node:child_process";
import type { AiConfig } from "../src/stores/use-config-store";
import type { VideoGenerationLog } from "../src/stores/use-video-generation-store";
import type { VideoGenerationResult, VideoGenerationTask } from "../src/services/api/video";

// Isolate browser-storage and API mocks from other suites.
if (!process.env.VIDEO_GENERATION_STORE_TEST_CHILD) {
    test("video completion updates shared results without reloading the page", () => {
        const child = spawnSync(process.execPath, ["test", import.meta.path], {
            cwd: import.meta.dir + "/..", encoding: "utf8",
            env: { ...process.env, VIDEO_GENERATION_STORE_TEST_CHILD: "1" },
        });
        expect(child.status, child.stdout + child.stderr).toBe(0);
    });
} else {
    const saved = new Map<string, VideoGenerationLog>();
    const removedKeys: string[] = [];
    const created: Array<{ config: AiConfig; prompt: string }> = [];
    const queries: Array<{ config: AiConfig; task: VideoGenerationTask; resolve: (result: VideoGenerationResult) => void; reject: (error: Error) => void }> = [];
    let finishSaving: (() => void) | undefined;
    let holdCompletedWrite = false;
    let ready = true;
    let creationError: Error | undefined;
    const config = {
        model: "provider::video", videoModel: "provider::video", size: "1280x720", vquality: "720",
        videoSeconds: "6", videoGenerateAudio: "true", videoWatermark: "false", videoMode: "frames",
        apiKey: "private-key", channels: [{ apiKey: "provider-private-key" }],
    } as AiConfig;
    mock.module("localforage", () => ({ default: { createInstance: () => ({
        iterate: async (callback: (value: VideoGenerationLog) => void) => { saved.forEach((value) => callback(structuredClone(value))); },
        setItem: async (key: string, value: VideoGenerationLog) => {
            if (holdCompletedWrite && value.status === "success") await new Promise<void>((resolve) => { finishSaving = resolve; });
            saved.set(key, structuredClone(value));
            return value;
        },
        removeItem: async (key: string) => { saved.delete(key); },
    }) } }));
    mock.module("../src/i18n", () => ({ default: { t: (key: string) => key, resolvedLanguage: "en" } }));
    mock.module("../src/stores/use-config-store", () => ({
        boolConfig: (value: string, fallback: boolean) => value === "true" ? true : value === "false" ? false : fallback,
        useConfigStore: { getState: () => ({ config, isAiConfigReady: () => ready }) },
    }));
    mock.module("../src/services/api/video", () => ({
        createVideoGenerationTask: async (config: AiConfig, prompt: string) => {
            created.push({ config, prompt });
            if (creationError) throw creationError;
            return { id: "remote-" + created.length, provider: "openai", model: config.model };
        },
        waitForVideoGenerationTask: (config: AiConfig, task: VideoGenerationTask) => new Promise<VideoGenerationResult>((resolve, reject) => queries.push({ config, task, resolve, reject })),
        storeGeneratedVideo: async (result: VideoGenerationResult) => ({
            url: "blob:" + result.url, storageKey: "video:" + result.url, width: 1280, height: 720, bytes: 42, mimeType: "video/mp4",
        }),
    }));
    mock.module("../src/services/file-storage", () => ({
        resolveMediaUrl: async (key: string, fallback: string) => key ? "blob:" + key : fallback,
        deleteStoredMedia: async (keys: string[]) => { removedKeys.push(...keys); },
    }));
    mock.module("../src/services/image-storage", () => ({
        resolveImageUrl: async (key: string, fallback: string) => key ? "blob:" + key : fallback,
        ensureImagePreview: async () => undefined,
    }));
    const { useVideoGenerationStore: store } = await import("../src/stores/use-video-generation-store");
    const { useWorkbenchAgentStore } = await import("../src/stores/use-workbench-agent-store");
    const references = [{ id: "ref", name: "ref.png", type: "image/png", dataUrl: "blob:ref", storageKey: "image:ref" }];
    const snapshot = { text: "Original video prompt", config, references };
    const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
    beforeEach(() => {
        store.setState({ prompt: "", references: [], logs: [], activeLogId: null });
        saved.clear(); created.length = 0; queries.length = 0; removedKeys.length = 0;
        holdCompletedWrite = false; finishSaving = undefined; ready = true; creationError = undefined;
    });

    test("restored tasks publish completion to the selected record with one query owner", async () => {
        const restored: VideoGenerationLog = {
            id: "restored", createdAt: Date.now(), title: "Restored", prompt: snapshot.text, time: "",
            model: config.model, config, references, durationMs: 0, size: "1280x720", resolution: "720", seconds: "6",
            status: "pending", task: { id: "original-task", provider: "gemini", model: config.model },
        };
        saved.set(restored.id, restored);
        await Promise.all([store.getState().loadLogs(), store.getState().loadLogs()]);
        expect(store.getState()).toMatchObject({ activeLogId: restored.id, prompt: snapshot.text });
        store.getState().selectLog(store.getState().logs[0]);
        await store.getState().loadLogs();
        expect(queries).toHaveLength(1);
        expect(queries[0].task).toEqual(restored.task);
        expect(created).toHaveLength(0);
        const observed: string[] = [];
        const unsubscribe = store.subscribe((state) => { observed.push(state.logs.find((log) => log.id === state.activeLogId)!.status); });
        queries[0].resolve({ url: "restored-video" });
        await tick();
        unsubscribe();
        expect(observed).toContain("success");
        expect(store.getState().logs[0].video?.url).toBe("blob:restored-video");
        expect(saved.get(restored.id)?.status).toBe("success");
        expect(queries).toHaveLength(1);
    });

    test("completion remains visible after all page subscriptions are removed and restored", async () => {
        store.getState().setPrompt(snapshot.text);
        store.getState().setReferences(references);
        const pending = store.getState().generate(snapshot);
        await tick();
        const id = store.getState().activeLogId!;
        const unsubscribe = store.subscribe(() => {});
        unsubscribe();
        await store.getState().loadLogs();
        expect(queries).toHaveLength(1);
        queries[0].resolve({ url: "completed" });
        await pending;
        const subscribeAgain = store.subscribe(() => {});
        expect(store.getState()).toMatchObject({ activeLogId: id, prompt: snapshot.text, references });
        expect(store.getState().logs[0]).toMatchObject({ status: "success", video: { url: "blob:completed" } });
        expect(saved.get(id)?.video?.url).toBe("");
        expect(JSON.stringify(saved.get(id))).not.toContain("private-key");
        subscribeAgain();
    });

    test("completion is published before a delayed local history write", async () => {
        const pending = store.getState().generate(snapshot);
        await tick();
        const id = store.getState().activeLogId!;
        holdCompletedWrite = true;
        queries[0].resolve({ url: "ready-before-save" });
        await tick();
        expect(store.getState().logs[0]).toMatchObject({ status: "success", video: { url: "blob:ready-before-save" } });
        expect(saved.get(id)?.status).toBe("pending");
        expect(finishSaving).toBeDefined();
        finishSaving!();
        await pending;
        expect(saved.get(id)?.status).toBe("success");
    });

    test("background completion preserves another draft and a different selected record", async () => {
        const pending = store.getState().generate(snapshot);
        await tick();
        const firstId = store.getState().activeLogId!;
        store.getState().createSession();
        store.getState().setPrompt("Another draft");
        queries[0].resolve({ url: "first" });
        await pending;
        expect(store.getState()).toMatchObject({ activeLogId: null, prompt: "Another draft" });
        const second = store.getState().generate({ ...snapshot, text: "Second video" });
        await tick();
        const secondId = store.getState().activeLogId!;
        store.getState().selectLog(store.getState().logs.find((log) => log.id === firstId)!);
        queries[1].resolve({ url: "second" });
        await second;
        expect(store.getState()).toMatchObject({ activeLogId: firstId, prompt: snapshot.text });
        expect(store.getState().logs.find((log) => log.id === firstId)?.video?.url).toBe("blob:first");
        expect(store.getState().logs.find((log) => log.id === secondId)?.video?.url).toBe("blob:second");
    });

    test("pending tasks reject duplicate starts and deletion, then update the Agent on completion", async () => {
        const taskId = useWorkbenchAgentStore.getState().dispatchVideo({ prompt: snapshot.text, run: true })!;
        const pending = store.getState().generate(snapshot, taskId);
        await tick();
        const id = store.getState().activeLogId!;
        await expect(store.getState().generate(snapshot)).rejects.toThrow("videoWorkbench.busy");
        await store.getState().deleteLogs([id]);
        expect(store.getState().logs).toHaveLength(1);
        expect(created).toHaveLength(1);
        queries[0].resolve({ url: "agent-result" });
        await pending;
        expect(useWorkbenchAgentStore.getState().tasks.find((task) => task.id === taskId)).toMatchObject({ status: "succeeded", successCount: 1 });
        await store.getState().deleteLogs([id]);
        expect(saved.has(id)).toBe(false);
        expect(store.getState().activeLogId).toBeNull();
        expect(removedKeys).toEqual(["video:agent-result"]);
    });

    test("task failures publish an error and release the generate control", async () => {
        const pending = store.getState().generate(snapshot);
        await tick();
        queries[0].reject(new Error("provider failed"));
        expect(await pending).toMatchObject({ status: "failed", error: "provider failed" });
        expect(store.getState().logs.some((log) => log.status === "pending")).toBe(false);
        expect(saved.get(store.getState().activeLogId!)?.error).toBe("provider failed");
    });

    test("submission failures finish the selected record without starting a query", async () => {
        creationError = new Error("submission failed");
        expect(await store.getState().generate(snapshot)).toMatchObject({ status: "failed", error: "submission failed" });
        expect(queries).toHaveLength(0);
        expect(store.getState().logs[0].status).toBe("failed");
    });
}
