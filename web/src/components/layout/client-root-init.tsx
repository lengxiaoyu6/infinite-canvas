"use client";

import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { usePathname } from "next/navigation";

import { fetchUserConfig } from "@/services/api/user-config";
import { App } from "antd";

import { replaceWorkflowChannels } from "@/services/workflow-channel-storage";
import { STORAGE_SYNC_FAILED_EVENT, defaultUserStorageProvider, defaultUserWebDAVStorageProvider, saveUserStorageProvider, saveUserWebDAVStorageProvider } from "@/services/image-storage";
import { useConfigStore, useIsModelConfigReady } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

export function ClientRootInit({ children }: { children: ReactNode }) {
    const { message } = App.useApp();
    const pathname = usePathname();
    const token = useUserStore((state) => state.token);
    const user = useUserStore((state) => state.user);
    const isUserReady = useUserStore((state) => state.isReady);
    const hydrateUser = useUserStore((state) => state.hydrateUser);
    const loadPublicSettings = useConfigStore((state) => state.loadPublicSettings);
    const channelMode = useConfigStore((state) => state.config.channelMode);
    const updateConfig = useConfigStore((state) => state.updateConfig);
    const switchModelConfigOwner = useConfigStore((state) => state.switchModelConfigOwner);
    const beginUserModelConfigLoad = useConfigStore((state) => state.beginUserModelConfigLoad);
    const applyUserModelConfig = useConfigStore((state) => state.applyUserModelConfig);
    const failUserModelConfigLoad = useConfigStore((state) => state.failUserModelConfigLoad);
    const isModelConfigReady = useIsModelConfigReady();
    const isLoginPage = pathname === "/login" || pathname === "/admin/login";
    const isTokenDanceCallback = pathname === "/tokendance/callback";
    const adminRemoteTokenRef = useRef("");

    useEffect(() => {
        const onSyncFailed = (event: Event) => {
            const detail = (event as CustomEvent<string>).detail;
            message.warning({ key: STORAGE_SYNC_FAILED_EVENT, content: `云端同步失败，已保留原始素材${detail ? `：${detail}` : ""}` });
        };
        window.addEventListener(STORAGE_SYNC_FAILED_EVENT, onSyncFailed);
        return () => window.removeEventListener(STORAGE_SYNC_FAILED_EVENT, onSyncFailed);
    }, [message]);

    useEffect(() => {
        void loadPublicSettings();
    }, [loadPublicSettings]);

    useEffect(() => {
        if (!isLoginPage) void hydrateUser();
    }, [hydrateUser, isLoginPage]);

    useEffect(() => {
        if (!token || user?.role !== "admin") {
            adminRemoteTokenRef.current = "";
            return;
        }
        if (isTokenDanceCallback) {
            adminRemoteTokenRef.current = token;
            return;
        }
        if (!isModelConfigReady || adminRemoteTokenRef.current === token) return;
        adminRemoteTokenRef.current = token;
        if (channelMode !== "remote") updateConfig("channelMode", "remote");
    }, [channelMode, isModelConfigReady, isTokenDanceCallback, token, updateConfig, user?.role]);

    useEffect(() => {
        if (!isUserReady) return;
        if (!token || !user?.id) {
            switchModelConfigOwner("");
            return;
        }
        const userId = user.id;
        switchModelConfigOwner(userId);
        if (isTokenDanceCallback) return;
        const accountToken = token;
        const loadVersion = beginUserModelConfigLoad(userId);
        if (!loadVersion) return;
        let canceled = false;
        void fetchUserConfig(accountToken)
            .then(async (payload) => {
                if (canceled || useUserStore.getState().token !== accountToken || useUserStore.getState().user?.id !== userId) return;
                const syncS3 = payload.modelConfig?.syncStorageConfig === true;
                const syncWebDAV = payload.modelConfig?.syncWebDAVStorageConfig === true;
                const { workflowChannels, ...modelConfig } = payload.modelConfig || {};
                let workflowsReady = true;
                if (workflowChannels !== undefined) {
                    try {
                        await replaceWorkflowChannels(userId, workflowChannels);
                    } catch {
                        workflowsReady = false;
                    }
                }
                if (canceled || useUserStore.getState().token !== accountToken || useUserStore.getState().user?.id !== userId || useConfigStore.getState().modelConfigLoadVersion !== loadVersion) return;
                applyUserModelConfig(userId, loadVersion, { ...modelConfig, workflowSyncTouched: workflowsReady });
                if (syncS3 && payload.storageProvider?.s3) {
                    saveUserStorageProvider({
                        ...defaultUserStorageProvider(),
                        ...payload.storageProvider.s3,
                        type: "s3",
                    });
                }
                if (syncWebDAV && payload.storageProvider?.webdav) {
                    saveUserWebDAVStorageProvider({
                        ...defaultUserWebDAVStorageProvider(),
                        ...payload.storageProvider.webdav,
                        type: "webdav",
                    });
                }
            })
            .catch(() => {
                failUserModelConfigLoad(userId, loadVersion);
            });
        return () => {
            canceled = true;
        };
    }, [applyUserModelConfig, beginUserModelConfigLoad, failUserModelConfigLoad, isTokenDanceCallback, isUserReady, switchModelConfigOwner, token, user?.id]);

    return <>{children}</>;
}
