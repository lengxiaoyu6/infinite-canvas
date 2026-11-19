import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import axios, { type InternalAxiosRequestConfig } from "axios";
import type { AiConfig } from "../src/stores/use-config-store";

const values = new Map<string, string>();
const originalStorage = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => values.set(key, value),
        removeItem: (key: string) => values.delete(key),
    },
});
afterAll(() => {
    if (originalStorage) Object.defineProperty(globalThis, "localStorage", originalStorage);
    else Reflect.deleteProperty(globalThis, "localStorage");
});

const { defaultConfig } = await import("../src/stores/use-config-store");
const { requestGeneration, requestEdit } = await import("../src/services/api/image");
const { getPluginTemplates, normalizePluginImages } = await import("../src/services/api/model-plugin");
const { buildGenerationConfig, imageExtension } = await import("../src/lib/canvas/canvas-generation-helpers");
const { buildImageGenerationMetadata } = await import("../src/lib/canvas/canvas-node-factory");
const { CanvasNodeType } = await import("../src/types/canvas");

const encodedGif = "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";
const gifBytes = Buffer.from(encodedGif, "base64");
const reference = { id: "ref", name: "ref.png", type: "image/png", dataUrl: "data:image/png;base64,aW1hZ2U=" };
const config: AiConfig = {
    ...defaultConfig,
    model: "test::gpt-image-test",
    imageModel: "test::gpt-image-test",
    imageOutputFormat: "gif",
    channels: [{ id: "test", name: "Test", baseUrl: "https://example.com", apiKey: "test-key", apiFormat: "openai", models: [{ name: "gpt-image-test", capability: "image" }] }],
};
const originalAdapter = axios.defaults.adapter;
const requests: InternalAxiosRequestConfig[] = [];
let payload: { output_format?: string; data: Array<{ b64_json: string }> };
beforeEach(() => {
    requests.length = 0;
    payload = { output_format: "gif", data: [{ b64_json: encodedGif }] };
    axios.defaults.adapter = async (request) => {
        requests.push(request);
        return { data: payload, status: 200, statusText: "OK", headers: {}, config: request };
    };
});
afterEach(() => { axios.defaults.adapter = originalAdapter; });

test("GIF generation sends the output format and preserves image bytes and MIME type", async () => {
    const images = await requestGeneration(config, "A green rectangle");
    const body = JSON.parse(requests[0].data);
    expect(requests[0].url).toBe("https://example.com/v1/images/generations");
    expect(body).toMatchObject({ output_format: "gif", frame_count: 9, n: 1, model: "gpt-image-test" });
    for (const key of ["stream", "partial_images", "output_compression", "response_format"]) expect(body).not.toHaveProperty(key);
    const blob = await (await fetch(images[0].dataUrl)).blob();
    expect(blob.type).toBe("image/gif");
    expect(Buffer.from(await blob.arrayBuffer())).toEqual(gifBytes);
});

test("GIF editing preserves single and multiple reference uploads", async () => {
    for (const references of [[reference], [reference, { ...reference, id: "ref-2" }]]) {
        const images = await requestEdit(config, "Animate the illustration", references);
        const form = requests.at(-1)!.data as FormData;
        expect(form.get("output_format")).toBe("gif");
        expect(form.get("frame_count")).toBe("9");
        expect(form.get("n")).toBe("1");
        expect(form.getAll(references.length === 1 ? "image" : "image[]")).toHaveLength(references.length);
        for (const key of ["stream", "partial_images", "mask", "output_compression", "response_format"]) expect(form.has(key)).toBe(false);
        expect(images[0].dataUrl).toStartWith("data:image/gif;base64,");
    }
});

test("GIF batches send the requested count in each generation or editing request", async () => {
    payload.data = Array.from({ length: 3 }, () => ({ b64_json: encodedGif }));
    expect(await requestGeneration({ ...config, count: "3" }, "Three images")).toHaveLength(3);
    expect(await requestEdit({ ...config, count: "3" }, "Three edits", [reference])).toHaveLength(3);
    expect(requests).toHaveLength(2);
    for (const request of requests) {
        const count = request.data instanceof FormData ? Number(request.data.get("n")) : JSON.parse(request.data).n;
        expect(count).toBe(3);
    }
});

test("response format takes precedence and missing response formats use the request format", async () => {
    payload.output_format = "png";
    expect((await requestGeneration(config, "Image"))[0].dataUrl).toStartWith("data:image/png;base64,");
    delete payload.output_format;
    expect((await requestGeneration(config, "Animation"))[0].dataUrl).toStartWith("data:image/gif;base64,");
    expect((await requestGeneration({ ...config, imageOutputFormat: defaultConfig.imageOutputFormat }, "Image"))[0].dataUrl).toStartWith("data:image/png;base64,");
    expect(JSON.parse(requests.at(-1)!.data).output_format).toBe("png");
});

test("Gemini GIF requests fail clearly before sending any request", async () => {
    const gemini: AiConfig = { ...config, channels: [{ ...config.channels[0], apiFormat: "gemini" }] };
    await expect(requestGeneration(gemini, "Animation")).rejects.toThrow("GIF");
    await expect(requestEdit(gemini, "Animation", [reference])).rejects.toThrow("GIF");
    expect(requests).toHaveLength(0);
});

test("an unsupported GIF response is surfaced without retrying as PNG", async () => {
    axios.defaults.adapter = async (request) => {
        requests.push(request);
        throw new Error("GIF output is unavailable for this model");
    };
    await expect(requestGeneration(config, "Animation")).rejects.toThrow("GIF output is unavailable");
    expect(requests).toHaveLength(1);
});

test("OpenAI script templates honor GIF for generation and editing", async () => {
    const script = getPluginTemplates().image[0].script;
    const scripted: AiConfig = { ...config, channels: [{ ...config.channels[0], models: [{ ...config.channels[0].models[0], script }] }] };
    expect((await requestGeneration(scripted, "Animation"))[0].dataUrl).toStartWith("data:image/gif;base64,");
    expect(JSON.parse(requests[0].data).output_format).toBe("gif");
    expect(JSON.parse(requests[0].data)).not.toHaveProperty("response_format");
    expect((await requestEdit(scripted, "Animation", [reference]))[0].dataUrl).toStartWith("data:image/gif;base64,");
    expect(requests[1].data.get("output_format")).toBe("gif");
    expect(requests[1].data.has("response_format")).toBe(false);
    expect(normalizePluginImages([{ b64_json: encodedGif }], "gif")[0]).toStartWith("data:image/gif;base64,");
});

test("canvas generation metadata retains GIF for retries and uses the correct download suffix", () => {
    const metadata = buildImageGenerationMetadata("generation", { ...config, imageFrameCount: "12" }, 1, []);
    const node = { id: "gif-node", type: CanvasNodeType.Image, title: "GIF", position: { x: 0, y: 0 }, width: 64, height: 32, metadata };
    expect(buildGenerationConfig({ ...config, imageOutputFormat: "png" }, node, "image").imageOutputFormat).toBe("gif");
    expect(buildGenerationConfig({ ...config, imageFrameCount: "2" }, node, "image").imageFrameCount).toBe("12");
    expect(imageExtension("blob:example", "image/gif")).toBe("gif");
    expect(imageExtension(`data:image/gif;base64,${encodedGif}`)).toBe("gif");
    expect(imageExtension("blob:example", "image/png")).toBe("png");
});

test("GIF frame selection reaches generation and editing APIs with and without scripts", async () => {
    const script = getPluginTemplates().image[0].script;
    const scripted: AiConfig = { ...config, channels: [{ ...config.channels[0], models: [{ ...config.channels[0].models[0], script }] }] };
    for (const base of [config, scripted]) {
        for (const frameCount of [2, 12, 16]) {
            const selected = { ...base, imageFrameCount: String(frameCount) };
            await requestGeneration(selected, "Animation");
            expect(JSON.parse(requests.at(-1)!.data).frame_count).toBe(frameCount);
            await requestEdit(selected, "Animation", [reference]);
            expect(requests.at(-1)!.data.get("frame_count")).toBe(String(frameCount));
        }
    }
});

test("PNG requests omit GIF frame counts with and without scripts", async () => {
    const script = getPluginTemplates().image[0].script;
    const scripted: AiConfig = { ...config, channels: [{ ...config.channels[0], models: [{ ...config.channels[0].models[0], script }] }] };
    for (const base of [config, scripted]) {
        const png: AiConfig = { ...base, imageOutputFormat: "png", imageFrameCount: "12" };
        await requestGeneration(png, "Image");
        expect(JSON.parse(requests.at(-1)!.data)).not.toHaveProperty("frame_count");
        await requestEdit(png, "Image", [reference]);
        expect(requests.at(-1)!.data.has("frame_count")).toBe(false);
    }
});
