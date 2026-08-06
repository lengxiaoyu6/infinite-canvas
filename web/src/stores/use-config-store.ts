"use client";

import { useMemo } from "react";
import { create } from "zustand";
import { persist } from "zustand/middleware";

import { apiGet } from "@/services/api/request";
import type { AdminPublicModelChannelInfo, AdminPublicSettings } from "@/services/api/admin";
import { useUserStore } from "@/stores/use-user-store";

export type LocalModelChannel = {
    id: string;
    systemChannelId: string;
    apiKey: string;
    models: string[];
};

export type VideoMultiPromptItem = { prompt: string; duration: string };
export type VideoElementReference = { id: string; kind: "image" | "video" | "audio"; name: string; type: string; dataUrl?: string; url?: string; storageKey?: string; bytes?: number; width?: number; height?: number; durationMs?: number };
export type VideoElementItem = { name: string; description: string; references: VideoElementReference[] };

export type AiConfig = {
    channelMode: "remote" | "local";
    baseUrl: string;
    apiKey: string;
    model: string;
    imageModel: string;
    videoModel: string;
    textModel: string;
    audioModel: string;
    audioVoice: string;
    audioFormat: string;
    audioSpeed: string;
    audioInstructions: string;
    videoSeconds: string;
    videoMode: string;
    videoNegativePrompt: string;
    videoMultiShot: string;
    videoShotType: string;
    videoMultiPrompt: VideoMultiPromptItem[];
    videoElementList: VideoElementItem[];
    vquality: string;
    videoGenerateAudio: string;
    videoWatermark: string;
    videoCharacterOrientation: string;
    systemPrompt: string;
    models: string[];
    imageModels: string[];
    videoModels: string[];
    textModels: string[];
    audioModels: string[];
    quality: string;
    size: string;
    videoSize: string;
    count: string;
    canvasImageCount: string;
    timeout: string;
    apiMode: string;
    streamImages: string;
    streamPartialImages: string;
    responseFormatB64Json: string;
    codexCli: string;
    systemPrompts: {
        image: string;
        video: string;
        text: string;
        workflow: string;
        workflowAgent: string;
    };
    localChannels: LocalModelChannel[];
    publicChannels: AdminPublicModelChannelInfo[];
    syncStorageConfig: boolean;
    syncWebDAVStorageConfig: boolean;
    activeChannelId: string;
    imageChannelId: string;
    videoChannelId: string;
    textChannelId: string;
    audioChannelId: string;
};

export const CONFIG_STORE_KEY = "infinite-canvas:ai_config_store";
export type ModelCapability = "image" | "video" | "text" | "audio";

export const defaultConfig: AiConfig = {
    channelMode: "local",
    baseUrl: "https://api.openai.com",
    apiKey: "",
    model: "gpt-image-2",
    imageModel: "gpt-image-2",
    videoModel: "grok-imagine-video",
    textModel: "gpt-5.5",
    audioModel: "gpt-4o-mini-tts",
    audioVoice: "alloy",
    audioFormat: "mp3",
    audioSpeed: "1",
    audioInstructions: "",
    videoSeconds: "6",
    videoMode: "std",
    videoNegativePrompt: "",
    videoMultiShot: "false",
    videoShotType: "intelligence",
    videoMultiPrompt: [{ prompt: "", duration: "1" }],
    videoElementList: [{ name: "", description: "", references: [] }],
    vquality: "720",
    videoGenerateAudio: "false",
    videoWatermark: "false",
    videoCharacterOrientation: "video",
    systemPrompt: "",
    models: [],
    imageModels: [],
    videoModels: [],
    textModels: [],
    audioModels: [],
    quality: "auto",
    size: "1:1",
    videoSize: "1280x720",
    count: "1",
    canvasImageCount: "1",
    timeout: "600",
    apiMode: "images",
    streamImages: "",
    streamPartialImages: "1",
    responseFormatB64Json: "",
    codexCli: "",
    systemPrompts: {
        image: "",
        video: "",
        text: "",
        workflow: "",
        workflowAgent: "",
    },
    localChannels: [],
    publicChannels: [],
    syncStorageConfig: false,
    syncWebDAVStorageConfig: false,
    activeChannelId: "",
    imageChannelId: "",
    videoChannelId: "",
    textChannelId: "",
    audioChannelId: "",
};

type ConfigStore = {
    config: AiConfig;
    modelConfigOwnerId: string;
    isModelConfigReady: boolean;
    modelConfigLoadFailed: boolean;
    modelConfigLoadVersion: number;
    anonymousConfig: AiConfig;
    publicSettings: AdminPublicSettings | null;
    isPublicSettingsLoading: boolean;
    isConfigOpen: boolean;
    shouldPromptContinue: boolean;
    updateConfig: <K extends keyof AiConfig>(key: K, value: AiConfig[K]) => void;
    switchModelConfigOwner: (userId: string) => void;
    beginUserModelConfigLoad: (userId: string) => number;
    applyUserModelConfig: (userId: string, loadVersion: number, config?: Partial<AiConfig>) => void;
    failUserModelConfigLoad: (userId: string, loadVersion: number) => void;
    loadPublicSettings: () => Promise<void>;
    isAiConfigReady: (config: AiConfig, model: string) => boolean;
    openConfigDialog: (shouldPromptContinue?: boolean) => void;
    setConfigDialogOpen: (isOpen: boolean) => void;
    clearPromptContinue: () => void;
};

function resolveEffectiveConfig(config: AiConfig, modelChannel: AdminPublicSettings["modelChannel"] | null, canUseRemoteChannel: boolean, canUsePersonalChannel: boolean) {
    const channelMode = canUseRemoteChannel && canUsePersonalChannel ? config.channelMode : canUseRemoteChannel ? "remote" : "local";
    const canUseChannelMode = channelMode === "remote" ? canUseRemoteChannel : canUsePersonalChannel;
    const availableModels = normalizeModelList(modelChannel?.availableModels || []);
    const availableModelSet = new Set(availableModels);
    const publicChannels = (canUseChannelMode ? modelChannel?.channels || config.publicChannels : [])
        .filter((channel) => channelMode === "local" || channel.hasSystemApiKey)
        .map((channel) => ({
            ...channel,
            models: normalizeModelList(channel.models).filter((model) => !availableModelSet.size || availableModelSet.has(model)),
        }))
        .filter((channel) => channel.models.length > 0);
    const models = normalizeModelList(publicChannels.flatMap((channel) => channel.models));
    const textModels = filterModelsByCapability(models, "text");
    const imageModels = filterModelsByCapability(models, "image");
    const videoModels = filterModelsByCapability(models, "video");
    const audioModels = filterModelsByCapability(models, "audio");
    const fallbackTextModel = validDefault(modelChannel?.defaultTextModel || "", textModels) || preferredModel(textModels, isTextModelName);
    const fallbackModel = validDefault(modelChannel?.defaultModel || "", textModels) || fallbackTextModel;
    const fallbackImageModel = validDefault(modelChannel?.defaultImageModel || "", imageModels) || preferredModel(imageModels, isImageModelName);
    const fallbackVideoModel = validDefault(modelChannel?.defaultVideoModel || "", videoModels) || preferredModel(videoModels, isVideoModelName);
    const fallbackAudioModel = preferredModel(audioModels, isAudioModelName);
    const resolved: AiConfig = {
        ...config,
        channelMode,
        localChannels: normalizeLocalChannels(config),
        models,
        imageModels,
        videoModels,
        textModels,
        audioModels,
        model: textModels.includes(config.model) ? config.model : fallbackModel,
        imageModel: imageModels.includes(config.imageModel) ? config.imageModel : fallbackImageModel,
        videoModel: videoModels.includes(config.videoModel) ? config.videoModel : fallbackVideoModel,
        textModel: textModels.includes(config.textModel) ? config.textModel : fallbackTextModel || fallbackModel,
        audioModel: audioModels.includes(config.audioModel) ? config.audioModel : fallbackAudioModel,
        systemPrompt: channelMode === "remote" ? modelChannel?.systemPrompt || "" : config.systemPrompt,
        publicChannels,
    };
    resolved.imageChannelId = channelIdForModel(resolved, resolved.imageModel, config.imageChannelId);
    resolved.videoChannelId = channelIdForModel(resolved, resolved.videoModel, config.videoChannelId);
    resolved.textChannelId = channelIdForModel(resolved, resolved.textModel, config.textChannelId);
    resolved.audioChannelId = channelIdForModel(resolved, resolved.audioModel, config.audioChannelId);
    if (channelMode === "local") {
        const channel = localChannelForActiveModel(resolved);
        resolved.baseUrl = channel?.baseUrl || "";
        resolved.apiKey = channel?.apiKey || "";
    }
    return resolved;
}

function validDefault(model: string, models: string[]) {
    return models.includes(model) ? model : "";
}

function preferredModel(models: string[], predicate: (model: string) => boolean) {
    return models.find(predicate) || "";
}

function isVideoModelName(model: string) {
    const value = model.toLowerCase();
    return (
        value.includes("video") ||
        value.includes("seedance") ||
        value.includes("sora") ||
        value.includes("veo") ||
        value.includes("kling") ||
        value.includes("hailuo") ||
        value.includes("minimax") ||
        value.includes("skyreels") ||
        value.includes("happyhorse") ||
        value.includes("runway") ||
        value.includes("aleph") ||
        value.includes("vidu") ||
        value.includes("pixverse") ||
        value.includes("omni-flash") ||
        value.includes("gemini-omni-video") ||
        value.includes("veo3.1") ||
        value.includes("veo-3.1") ||
        value.includes("infinitalk") ||
        value.includes("wan2-5") ||
        value.includes("wan2.5") ||
        value.includes("wan2-6") ||
        value.includes("wan2.6") ||
        value.includes("wan2-7") ||
        value.includes("wan2.7") ||
        value.includes("wan2-7-r2v") ||
        value.includes("wan2.7-r2v") ||
        value.includes("wan2-7-videoedit") ||
        value.includes("wan2.7-videoedit") ||
        value.includes("wan/2-5") ||
        value.includes("wan/2-6") ||
        value.includes("wan/2-7-text-to-video") ||
        value.includes("wan/2-7-image-to-video") ||
        value.includes("wan/2-7-videoedit") ||
        value.includes("wan/2-7-r2v") ||
        (value.includes("grok-imagine") && (value.includes("/upscale") || value.includes("/extend")))
    );
}

function isImageModelName(model: string) {
    const value = model.toLowerCase();
    return !isVideoModelName(model) && !isAudioModelName(model) && (
        value.includes("image") ||
        value.includes("nano-banana") ||
        value.includes("seedream") ||
        value.includes("gpt-image") ||
        value.includes("dall-e") ||
        value.includes("dalle") ||
        value.includes("imagen") ||
        value.includes("gemini-2.5-flash") ||
        value.includes("gemini-3-pro") ||
        value.includes("gemini-3.1-flash") ||
        value.includes("flux") ||
        value.includes("kontext") ||
        value.includes("4o-image") ||
        value.includes("4o image") ||
        value.includes("gpt-4o-image") ||
        value.includes("z-image") ||
        value.includes("qwen/image") ||
        value.includes("qwen2/image") ||
        value.includes("qwen/text-to-image") ||
        value.includes("qwen2/text-to-image") ||
        value.includes("ideogram") ||
        value.includes("recraft") ||
        value.includes("sdxl") ||
        value.includes("stable-diffusion") ||
        value.includes("midjourney") ||
        value.includes("wan2-7-image") ||
        value.includes("wan2.7-image") ||
        value.includes("wan/2-7-image") ||
        value.includes("topaz/image") ||
        value.includes("gemini-omni-character") ||
        (value.includes("grok-imagine") && !value.includes("video"))
    );
}

function isAudioModelName(model: string) {
    const value = model.toLowerCase();
    return value.includes("audio") || value.includes("tts") || value.includes("speech") || value.includes("voice") || value.includes("music") || value.includes("sound") || value.includes("elevenlabs") || value.includes("suno") || value.includes("lyrics") || value.includes("vocal") || value.includes("midi") || value.includes("wav");
}

function isTextModelName(model: string) {
    return !isImageModelName(model) && !isVideoModelName(model) && !isAudioModelName(model);
}

export function modelMatchesCapability(model: string, capability?: ModelCapability) {
    if (!capability) return true;
    if (capability === "image") return isImageModelName(model);
    if (capability === "video") return isVideoModelName(model);
    if (capability === "audio") return isAudioModelName(model);
    return isTextModelName(model);
}

export function filterModelsByCapability(models: string[], capability?: ModelCapability) {
    return capability ? models.filter((model) => modelMatchesCapability(model, capability)) : models;
}

export function selectableModelsByCapability(config: AiConfig, capability?: ModelCapability) {
    return filterModelsByCapability(config.models, capability);
}

function hasCompleteAiConfig(config: AiConfig, model: string) {
    const channel = localChannelForActiveModel({ ...config, model });
    return Boolean(model.trim()) && (config.channelMode === "remote" || Boolean(channel?.baseUrl.trim() && channel?.apiKey.trim()));
}

function isModelConfigReadyForSession(modelConfigOwnerId: string, isModelConfigReady: boolean, token: string, userId: string | undefined, isUserReady: boolean) {
    const sessionOwnerId = token && userId ? userId : "";
    return isUserReady && isModelConfigReady && modelConfigOwnerId === sessionOwnerId;
}

export const useConfigStore = create<ConfigStore>()(
    persist(
        (set, get) => ({
            config: defaultConfig,
            modelConfigOwnerId: "",
            isModelConfigReady: true,
            modelConfigLoadFailed: false,
            modelConfigLoadVersion: 0,
            anonymousConfig: configForBrowserStorage(defaultConfig),
            publicSettings: null,
            isPublicSettingsLoading: false,
            isConfigOpen: false,
            shouldPromptContinue: false,
            updateConfig: (key, value) =>
                set((state) => {
                    const config = { ...state.config, [key]: value };
                    return {
                        config,
                        ...(!state.modelConfigOwnerId ? { anonymousConfig: configForBrowserStorage(config) } : {}),
                    };
                }),
            switchModelConfigOwner: (userId) =>
                set((state) => {
                    const ownerId = userId.trim();
                    if (state.modelConfigOwnerId === ownerId) return state;
                    const anonymousConfig = state.modelConfigOwnerId ? state.anonymousConfig : configForBrowserStorage(state.config);
                    return {
                        modelConfigOwnerId: ownerId,
                        isModelConfigReady: !ownerId,
                        modelConfigLoadFailed: false,
                        modelConfigLoadVersion: state.modelConfigLoadVersion + 1,
                        anonymousConfig,
                        config: ownerId ? configForBrowserStorage(defaultConfig) : anonymousConfig,
                    };
                }),
            beginUserModelConfigLoad: (userId) => {
                const ownerId = userId.trim();
                const state = get();
                if (!ownerId || state.modelConfigOwnerId !== ownerId) return 0;
                const loadVersion = state.modelConfigLoadVersion + 1;
                set({ modelConfigLoadVersion: loadVersion, isModelConfigReady: false, modelConfigLoadFailed: false });
                return loadVersion;
            },
            applyUserModelConfig: (userId, loadVersion, config) =>
                set((state) => {
                    if (!userId || state.modelConfigOwnerId !== userId || state.modelConfigLoadVersion !== loadVersion) return state;
                    const remoteConfig = config || {};
                    return {
                        isModelConfigReady: true,
                        modelConfigLoadFailed: false,
                        config: {
                            ...defaultConfig,
                            ...remoteConfig,
                            systemPrompts: { ...defaultConfig.systemPrompts, ...(remoteConfig.systemPrompts || {}) },
                            localChannels: normalizeLocalChannels(remoteConfig),
                            baseUrl: "",
                            apiKey: "",
                            publicChannels: [],
                            syncStorageConfig: remoteConfig.syncStorageConfig === true,
                            syncWebDAVStorageConfig: remoteConfig.syncWebDAVStorageConfig === true,
                        },
                    };
                }),
            failUserModelConfigLoad: (userId, loadVersion) =>
                set((state) => state.modelConfigOwnerId === userId && state.modelConfigLoadVersion === loadVersion && !state.isModelConfigReady ? { modelConfigLoadFailed: true } : state),
            loadPublicSettings: async () => {
                if (get().isPublicSettingsLoading) return;
                set({ isPublicSettingsLoading: true });
                try {
                    set({ publicSettings: await apiGet<AdminPublicSettings>("/api/settings") });
                } finally {
                    set({ isPublicSettingsLoading: false });
                }
            },
            isAiConfigReady: (config, model) => {
                const state = get();
                const session = useUserStore.getState();
                return isModelConfigReadyForSession(state.modelConfigOwnerId, state.isModelConfigReady, session.token, session.user?.id, session.isReady)
                    && hasCompleteAiConfig(config, model);
            },
            openConfigDialog: (shouldPromptContinue = false) => set({ isConfigOpen: true, shouldPromptContinue }),
            setConfigDialogOpen: (isConfigOpen) => set({ isConfigOpen }),
            clearPromptContinue: () => set({ shouldPromptContinue: false }),
        }),
        {
            name: CONFIG_STORE_KEY,
            partialize: (state) => ({
                config: configForBrowserStorage(state.modelConfigOwnerId ? state.anonymousConfig : state.config),
            }),
            merge: (persisted, current) => {
                const persistedState = (persisted || {}) as Partial<ConfigStore>;
                const persistedConfig = (persistedState.config || {}) as Partial<AiConfig>;
                const config = { ...defaultConfig, ...persistedConfig };
                const localChannels = normalizeLocalChannels(config);
                const localModels = normalizeModelList(config.models);
                const restoredConfig: AiConfig = {
                    ...config,
                    localChannels,
                    models: localModels,
                    baseUrl: "",
                    apiKey: "",
                    publicChannels: [],
                    imageChannelId: config.imageChannelId || localChannels[0]?.id || "",
                    videoChannelId: config.videoChannelId || localChannels[0]?.id || "",
                    textChannelId: config.textChannelId || localChannels[0]?.id || "",
                    audioChannelId: config.audioChannelId || localChannels[0]?.id || "",
                    activeChannelId: config.activeChannelId || "",
                    syncStorageConfig: config.syncStorageConfig === true,
                    syncWebDAVStorageConfig: config.syncWebDAVStorageConfig === true,
                    channelMode: config.channelMode || "remote",
                    imageModel: config.imageModel || config.model,
                    videoModel: config.videoModel || "grok-imagine-video",
                    textModel: config.textModel || config.model,
                    audioModel: config.audioModel || defaultConfig.audioModel,
                    audioVoice: config.audioVoice || defaultConfig.audioVoice,
                    audioFormat: config.audioFormat || defaultConfig.audioFormat,
                    audioSpeed: config.audioSpeed || defaultConfig.audioSpeed,
                    systemPrompts: { ...defaultConfig.systemPrompts, ...(config.systemPrompts || {}) },
                    audioInstructions: config.audioInstructions || "",
                    videoSeconds: config.videoSeconds || "6",
                    videoMode: config.videoMode || "std",
                    videoNegativePrompt: config.videoNegativePrompt || "",
                    videoMultiShot: config.videoMultiShot || "false",
                    videoShotType: config.videoShotType || "intelligence",
                    videoMultiPrompt: Array.isArray(config.videoMultiPrompt) && config.videoMultiPrompt.length ? config.videoMultiPrompt : defaultConfig.videoMultiPrompt,
                    videoElementList: Array.isArray(config.videoElementList) && config.videoElementList.length ? config.videoElementList : defaultConfig.videoElementList,
                    vquality: config.vquality || "720",
                    videoGenerateAudio: config.videoGenerateAudio || "false",
                    videoWatermark: config.videoWatermark || "false",
                    videoCharacterOrientation: config.videoCharacterOrientation === "image" ? "image" : "video",
                    canvasImageCount: config.canvasImageCount || "1",
                    imageModels: filterModelsByCapability(localModels, "image"),
                    videoModels: filterModelsByCapability(localModels, "video"),
                    textModels: filterModelsByCapability(localModels, "text"),
                    audioModels: filterModelsByCapability(localModels, "audio"),
                };
                return { ...current, modelConfigOwnerId: "", isModelConfigReady: true, modelConfigLoadFailed: false, modelConfigLoadVersion: 0, anonymousConfig: restoredConfig, config: restoredConfig };
            },
        },
    ),
);

function normalizeModelList(models: string[]) {
    return Array.from(new Set((models || []).map((model) => model.trim()).filter(Boolean)));
}

export function useEffectiveConfig() {
    const config = useConfigStore((state) => state.config);
    const isCurrentModelConfigReady = useIsModelConfigReady();
    const modelChannel = useConfigStore((state) => state.publicSettings?.modelChannel || null);
    const token = useUserStore((state) => state.token);
    const user = useUserStore((state) => state.user);
    const canUseRemoteChannel = Boolean(token && user && (user.role === "admin" || modelChannel?.allowUserRemoteChannel === true));
    const canUsePersonalChannel = modelChannel?.allowCustomChannel === true;
    const sourceConfig = isCurrentModelConfigReady ? config : defaultConfig;
    return useMemo(() => resolveEffectiveConfig(sourceConfig, modelChannel, canUseRemoteChannel, canUsePersonalChannel), [canUsePersonalChannel, canUseRemoteChannel, modelChannel, sourceConfig]);
}

export function useIsModelConfigReady() {
    const modelConfigOwnerId = useConfigStore((state) => state.modelConfigOwnerId);
    const isModelConfigReady = useConfigStore((state) => state.isModelConfigReady);
    const token = useUserStore((state) => state.token);
    const userId = useUserStore((state) => state.user?.id);
    const isUserReady = useUserStore((state) => state.isReady);
    return isModelConfigReadyForSession(modelConfigOwnerId, isModelConfigReady, token, userId, isUserReady);
}

export function buildApiUrl(baseUrl: string, path: string) {
    let normalizedBaseUrl = baseUrl.trim().replace(/\/+$/, "");
    normalizedBaseUrl = normalizeArkPlanBaseUrl(normalizedBaseUrl);
    const lowerBaseUrl = normalizedBaseUrl.toLowerCase();
    const apiBaseUrl = lowerBaseUrl.endsWith("/v1") || lowerBaseUrl.endsWith("/api/v3") || lowerBaseUrl.endsWith("/api/plan/v3") ? normalizedBaseUrl : `${normalizedBaseUrl}/v1`;
    return `${apiBaseUrl}${path}`;
}

function normalizeArkPlanBaseUrl(baseUrl: string) {
    try {
        const url = new URL(baseUrl);
        const path = url.pathname.replace(/\/+$/, "");
        const lowerPath = path.toLowerCase();
        const arkPlanIndex = lowerPath.indexOf("/api/plan/v3");
        if (arkPlanIndex < 0) return baseUrl;
        const end = arkPlanIndex + "/api/plan/v3".length;
        if (lowerPath.length !== end && lowerPath[end] !== "/") return baseUrl;
        url.pathname = path.slice(0, end);
        url.search = "";
        url.hash = "";
        return url.toString().replace(/\/+$/, "");
    } catch {
        return baseUrl;
    }
}

export function normalizeLocalChannels(config: Partial<AiConfig>) {
    const channels = Array.isArray(config.localChannels) ? config.localChannels : [];
    return channels.map((channel, index) => {
        const legacy = channel as LocalModelChannel & { systemChannelId?: string };
        const systemChannelId = legacy.systemChannelId || channel.id || `local-${index + 1}`;
        return {
            id: channel.id || systemChannelId,
            systemChannelId,
            apiKey: channel.apiKey || "",
            models: Array.isArray(channel.models) ? channel.models.filter(Boolean) : [],
        };
    });
}

function configForBrowserStorage(config: AiConfig): AiConfig {
    return {
        ...config,
        localChannels: normalizeLocalChannels(config),
        baseUrl: "",
        apiKey: "",
        publicChannels: [],
    };
}

export function channelIdForActiveModel(config: AiConfig) {
    let preferredId = config.activeChannelId;
    if (modelMatchesCapability(config.model, "image")) preferredId = config.imageChannelId;
    else if (modelMatchesCapability(config.model, "video")) preferredId = config.videoChannelId;
    else if (modelMatchesCapability(config.model, "audio")) preferredId = config.audioChannelId;
    else if (modelMatchesCapability(config.model, "text")) preferredId = config.textChannelId;
    return channelIdForModel(config, config.model, preferredId);
}

export function localChannelForActiveModel(config: AiConfig) {
    const channelId = channelIdForActiveModel(config);
    const systemChannel = config.publicChannels.find((channel) => channel.id === channelId && channel.models.includes(config.model));
    if (!systemChannel) return undefined;
    const personalChannel = normalizeLocalChannels(config).find((channel) => channel.systemChannelId === systemChannel.id);
    return {
        ...systemChannel,
        apiKey: personalChannel?.apiKey || "",
    };
}

function channelIdForModel(config: AiConfig, model: string, preferredId: string) {
    if (!model) return "";
    if (preferredId && config.publicChannels.some((channel) => channel.id === preferredId && channel.models.includes(model))) return preferredId;
    return config.publicChannels.find((channel) => channel.models.includes(model))?.id || "";
}

