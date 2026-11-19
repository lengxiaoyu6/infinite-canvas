import { beforeEach, expect, mock, test } from "bun:test";
import { spawnSync } from "node:child_process";
import type { AiConfig } from "../src/stores/use-config-store";
import type { GenerationLog } from "../src/stores/use-image-generation-store";

// Keep module mocks out of the other API and image-storage tests in the suite.
if (!process.env.IMAGE_GENERATION_STORE_TEST_CHILD) {
    test("image generation survives page changes and persists task progress", () => {
        const child = spawnSync(process.execPath, ["test", import.meta.path], {
            cwd: import.meta.dir + "/..", encoding: "utf8",
            env: { ...process.env, IMAGE_GENERATION_STORE_TEST_CHILD: "1" },
        });
        expect(child.status, child.stdout + child.stderr).toBe(0);
    });
} else {
    const saved = new Map<string, GenerationLog>();
    const removedKeys: string[] = [];
    let storageError = false;
    type ImageResponse = { id: string; dataUrl: string }[];
    const requests: Array<{ config: AiConfig; prompt: string; edit: boolean; resolve: (value: ImageResponse) => void; reject: (error: Error) => void }> = [];
    const request = (config: AiConfig, prompt: string, edit = false) => new Promise<ImageResponse>((resolve, reject) => requests.push({ config, prompt, edit, resolve, reject }));
    mock.module("localforage", () => ({ default: { createInstance: () => ({
        iterate: async (callback: (value: GenerationLog) => void) => { saved.forEach((value) => callback(structuredClone(value))); },
        setItem: async (key: string, value: GenerationLog) => {
            if (storageError) throw new Error("storage unavailable");
            saved.set(key, structuredClone(value));
            return value;
        },
        removeItem: async (key: string) => { saved.delete(key); },
    }) } }));
    mock.module("../src/i18n", () => ({ default: { t: (key: string) => key, resolvedLanguage: "en" } }));
    mock.module("../src/stores/use-config-store", () => ({ defaultConfig: { imageFrameCount: "9" } }));
    mock.module("../src/services/api/image", () => ({
        requestGeneration: (config: AiConfig, prompt: string) => request(config, prompt),
        requestEdit: (config: AiConfig, prompt: string) => request(config, prompt, true),
    }));
    mock.module("../src/services/image-storage", () => ({
        uploadImage: async (url: string) => ({ url: "blob:" + url, storageKey: "image:" + url, width: 64, height: 48, bytes: 42, mimeType: "image/gif" }),
        resolveImageUrl: async (key?: string, fallback = "") => key ? "blob:" + key : fallback,
        ensureImagePreview: async () => undefined,
        deleteStoredImages: async (keys: string[]) => { removedKeys.push(...keys); },
    }));
    const { useImageGenerationStore: store } = await import("../src/stores/use-image-generation-store");
    const { useWorkbenchAgentStore } = await import("../src/stores/use-workbench-agent-store");
    const config = {
        model: "provider::image", imageModel: "provider::image", quality: "high", size: "1024x1024", count: "1",
        imageOutputFormat: "gif", imageFrameCount: "12", apiKey: "private-key", channels: [{ apiKey: "channel-private-key" }],
    } as AiConfig;
    const references = [{ id: "ref", name: "ref.png", type: "image/png", dataUrl: "blob:ref", storageKey: "image:ref" }];
    const snapshot = { text: "Original prompt", config, references: [] };
    const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
    beforeEach(() => {
        store.setState({ prompt: "", references: [], logs: [], resultsByLogId: {}, activeLogId: null, startedAt: 0 });
        saved.clear(); requests.length = 0; removedKeys.length = 0; storageError = false;
    });

    test("reload preserves unknown provider outcomes and requires confirmation before resubmission", async () => {
        saved.set("interrupted", {
            id: "interrupted", createdAt: 1, prompt: "saved prompt", model: config.model, status: "pending",
            config, imageCount: 4, successCount: 1, failCount: 1, unknownCount: 0, error: "provider failed", references,
            images: [{ id: "finished", dataUrl: "", storageKey: "image:finished", durationMs: 10, width: 64, height: 48, bytes: 42 }],
        } as GenerationLog);
        await store.getState().loadLogs();
        const log = store.getState().logs[0];
        expect(log).toMatchObject({ status: "unknown", successCount: 1, failCount: 1, unknownCount: 2, error: "provider failed" });
        expect(log.images[0].dataUrl).toBe("blob:image:finished");
        expect(log.references[0].dataUrl).toBe("blob:image:ref");
        expect(store.getState().resultsByLogId[log.id].map((item) => item.status)).toEqual(["success", "failed", "unknown", "unknown"]);
        expect(saved.get(log.id)).toMatchObject({ status: "unknown", failCount: 1, unknownCount: 2 });
        expect(requests).toHaveLength(0);

        await expect(store.getState().retryResult(log.id, 2, config)).rejects.toThrow("imageWorkbench.resubmitWarning");
        expect(requests).toHaveLength(0);
        expect(store.getState().logs[0]).toMatchObject({ status: "unknown", unknownCount: 2 });
        const retry = store.getState().retryResult(log.id, 2, config, true);
        await tick();
        expect(requests).toHaveLength(1);
        expect(requests[0]).toMatchObject({ prompt: "saved prompt", edit: true });
        expect(saved.get(log.id)).toMatchObject({ status: "pending", failCount: 1, unknownCount: 1 });
        requests[0].resolve([{ id: "resubmitted", dataUrl: "resubmitted" }]);
        expect(await retry).toMatchObject({ status: "unknown", successCount: 2, failCount: 1, unknownCount: 1 });
        expect(store.getState().resultsByLogId[log.id].map((item) => item.status)).toEqual(["success", "failed", "success", "unknown"]);

        // A new module instance models a fresh page using the same persistent storage.
        const { useImageGenerationStore: reloaded } = await import("../src/stores/use-image-generation-store" + "?reloaded");
        await reloaded.getState().loadLogs();
        expect(reloaded.getState().logs[0]).toMatchObject({ status: "unknown", successCount: 2, failCount: 1, unknownCount: 1 });
        expect(reloaded.getState().resultsByLogId[log.id].map((item: { status: string }) => item.status)).toEqual(["success", "success", "failed", "unknown"]);
        expect(requests).toHaveLength(1);
    });

    test("pending record and each partial result are saved without provider credentials", async () => {
        const pending = store.getState().generate({ ...snapshot, references }, 2);
        await tick();
        const id = store.getState().activeLogId!;
        expect(store.getState().logs[0]).toMatchObject({ status: "pending", successCount: 0, failCount: 0, imageCount: 2 });
        expect(saved.get(id)?.status).toBe("pending");
        expect(JSON.stringify(saved.get(id))).not.toContain("private-key");
        expect(saved.get(id)?.references[0].dataUrl).toBe("");
        expect(requests).toHaveLength(2);
        expect(requests.every((item) => item.edit)).toBe(true);
        await store.getState().deleteLogs([id]);
        expect(store.getState().logs).toHaveLength(1);
        requests[0].resolve([{ id: "one", dataUrl: "one" }]);
        await tick();
        expect(saved.get(id)).toMatchObject({ status: "pending", successCount: 1, failCount: 0 });
        expect(saved.get(id)?.images[0].dataUrl).toBe("");
        expect(store.getState().resultsByLogId[id][0].image?.dataUrl).toBe("blob:one");
        requests[1].resolve([{ id: "two", dataUrl: "two" }]);
        await pending;
        expect(saved.get(id)).toMatchObject({ status: "success", successCount: 2, failCount: 0 });
        expect(store.getState().resultsByLogId[id].map((item) => item.status)).toEqual(["success", "success"]);
        await store.getState().deleteLogs([id]);
        expect(saved.has(id)).toBe(false);
        expect(store.getState().activeLogId).toBeNull();
        expect(removedKeys).toEqual(["image:one", "image:two"]);
    });

    test("removing and restoring page subscriptions keeps the active task and draft", async () => {
        store.getState().setPrompt(snapshot.text);
        store.getState().setReferences(references);
        const pending = store.getState().generate(snapshot, 1);
        await tick();
        const id = store.getState().activeLogId!;
        const unsubscribe = store.subscribe(() => {});
        unsubscribe();
        await store.getState().loadLogs();
        expect(store.getState()).toMatchObject({ activeLogId: id, prompt: snapshot.text, references });
        requests[0].resolve([{ id: "away", dataUrl: "away" }]);
        await pending;
        const subscribeAgain = store.subscribe(() => {});
        expect(store.getState().logs[0].status).toBe("success");
        expect(store.getState().resultsByLogId[id][0].image?.id).toBe("away");
        subscribeAgain();
    });

    test("background completion leaves another draft and selected record unchanged", async () => {
        const pending = store.getState().generate(snapshot, 1);
        await tick();
        const id = store.getState().activeLogId!;
        store.getState().createSession();
        store.getState().setPrompt("another draft");
        requests[0].resolve([{ id: "background", dataUrl: "background" }]);
        await pending;
        expect(store.getState()).toMatchObject({ activeLogId: null, prompt: "another draft" });
        expect(store.getState().resultsByLogId[id][0].image?.id).toBe("background");
        const second = store.getState().generate({ ...snapshot, text: "Second prompt" }, 1);
        await tick();
        const secondId = store.getState().activeLogId!;
        store.getState().selectLog(store.getState().logs.find((log) => log.id === id)!);
        requests[1].resolve([{ id: "second", dataUrl: "second" }]);
        await second;
        expect(store.getState()).toMatchObject({ activeLogId: id, prompt: snapshot.text });
        expect(store.getState().resultsByLogId[id][0].image?.id).toBe("background");
        expect(store.getState().resultsByLogId[secondId][0].image?.id).toBe("second");
    });

    test("retry updates only the failed slot using the record settings", async () => {
        const pending = store.getState().generate(snapshot, 2);
        await tick();
        requests[0].resolve([{ id: "keep", dataUrl: "keep" }]);
        requests[1].reject(new Error("provider failed"));
        const log = await pending;
        expect(log).toMatchObject({ successCount: 1, failCount: 1 });
        const retry = store.getState().retryResult(log.id, 1, { ...config, imageOutputFormat: "png", imageFrameCount: "9" });
        await tick();
        expect(requests).toHaveLength(3);
        expect(requests[2].config).toMatchObject({ imageOutputFormat: "gif", imageFrameCount: "12", count: "1" });
        expect(requests[2].prompt).toBe(snapshot.text);
        expect(saved.get(log.id)).toMatchObject({ status: "pending", successCount: 1, failCount: 0 });
        expect(store.getState().resultsByLogId[log.id][0].image?.id).toBe("keep");
        requests[2].resolve([{ id: "retried", dataUrl: "retried" }]);
        expect(await retry).toMatchObject({ status: "success", successCount: 2, failCount: 0 });
        expect(store.getState().logs).toHaveLength(1);
        expect(saved.get(log.id)?.images.map((image) => image.id)).toEqual(["keep", "retried"]);
    });

    test("duplicate starts are rejected and agent task completes without a page subscriber", async () => {
        const taskId = useWorkbenchAgentStore.getState().dispatchImage({ prompt: snapshot.text, run: true })!;
        const pending = store.getState().generate(snapshot, 1, taskId);
        await tick();
        await expect(store.getState().generate(snapshot, 1)).rejects.toThrow("imageWorkbench.busy");
        expect(requests).toHaveLength(1);
        requests[0].reject(new Error("provider failed"));
        expect(await pending).toMatchObject({ status: "failed", successCount: 0, failCount: 1 });
        expect(useWorkbenchAgentStore.getState().tasks.find((task) => task.id === taskId)).toMatchObject({ status: "failed", error: "provider failed" });
    });

    test("a storage failure ends the task without sending a request", async () => {
        storageError = true;
        await expect(store.getState().generate(snapshot, 1)).rejects.toThrow("storage unavailable");
        expect(store.getState().logs[0]).toMatchObject({ status: "failed", failCount: 1 });
        expect(requests).toHaveLength(0);
    });
}
