"use client";

import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { usePathname } from "next/navigation";

import { fetchUserConfig } from "@/services/api/user-config";
import { defaultUserStorageProvider, defaultUserWebDAVStorageProvider, saveUserStorageProvider, saveUserWebDAVStorageProvider } from "@/services/image-storage";
import { useConfigStore, type AiConfig } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

export function ClientRootInit({ children }: { children: ReactNode }) {
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
    const adminRemoteTokenRef = useRef("");

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
        if (!isModelConfigReady || adminRemoteTokenRef.current === token) return;
        adminRemoteTokenRef.current = token;
        if (channelMode !== "remote") updateConfig("channelMode", "remote");
    }, [channelMode, isModelConfigReady, token, updateConfig, user?.role]);

    useEffect(() => {
        if (!isUserReady) return;
        if (!token || !user?.id) {
            switchModelConfigOwner("");
            return;
        }
        const userId = user.id;
        switchModelConfigOwner(userId);
        const loadVersion = beginUserModelConfigLoad(userId);
        if (!loadVersion) return;
        let canceled = false;
        void fetchUserConfig(token)
            .then((payload) => {
                const syncS3 = payload.modelConfig?.syncStorageConfig === true;
                const syncWebDAV = payload.modelConfig?.syncWebDAVStorageConfig === true;
                if (payload.modelConfig) {
                    Object.entries(payload.modelConfig)
                        .forEach(([key, value]) => updateConfig(key as keyof AiConfig, value as never));
                }
                updateConfig("syncStorageConfig", syncS3);
                updateConfig("syncWebDAVStorageConfig", syncWebDAV);
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
    }, [applyUserModelConfig, beginUserModelConfigLoad, failUserModelConfigLoad, isUserReady, switchModelConfigOwner, token, user?.id]);

    return <>{children}</>;
}
