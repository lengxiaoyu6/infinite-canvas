"use client";

import { App, Button, Result, Spin } from "antd";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";

import type { TokenDanceOAuthState } from "@/lib/tokendance-oauth";
import { fetchUserConfig, syncUserModelConfig } from "@/services/api/user-config";
import { normalizeLocalChannels, useConfigStore } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

export default function TokenDanceCallbackPage() {
    const { message } = App.useApp();
    const router = useRouter();
    const started = useRef(false);
    const [error, setError] = useState("");
    const isUserReady = useUserStore((state) => state.isReady);

    useEffect(() => {
        if (!isUserReady || started.current) return;
        started.current = true;

        void (async () => {
            const session = useUserStore.getState();
            const accountToken = session.token;
            const ownerId = accountToken ? session.user?.id || "" : "";
            const assertSession = () => {
                const current = useUserStore.getState();
                if (current.token !== accountToken || (current.token ? current.user?.id || "" : "") !== ownerId) {
                    throw new Error("登录状态已变化，请重新登录");
                }
            };
            try {
                const params = new URLSearchParams(window.location.search);
                const code = params.get("code");
                const flow = params.get("flow");
                const storageKey = flow ? `tokendance:oauth:${flow}` : "";
                const raw = storageKey ? sessionStorage.getItem(storageKey) : null;

                if (!code || !flow || !raw) throw new Error("授权信息已失效，请重新登录");

                const state = JSON.parse(raw) as TokenDanceOAuthState;
                if (!state.verifier || (state.target !== "local" && state.target !== "admin")) {
                    throw new Error("授权信息不完整，请重新登录");
                }
                if (state.ownerId !== ownerId || accountToken && !ownerId) {
                    throw new Error("授权账号已变化，请重新登录");
                }
                if (state.target === "admin" && session.user?.role !== "admin") {
                    throw new Error("后台渠道授权需要管理员账号");
                }

                const returnTo = typeof state.returnTo === "string"
                    ? state.returnTo.replace(/[\t\n\r]/g, "")
                    : "/";
                const safeReturnTo = returnTo.startsWith("/") && !returnTo.startsWith("//") && !returnTo.startsWith("/\\")
                    ? returnTo
                    : "/";

                const response = await fetch("https://tokendance.space/portal/api/v1/auth/keys", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({
                        code,
                        code_verifier: state.verifier,
                        code_challenge_method: "S256",
                    }),
                });
                const data = await response.json().catch(() => ({})) as {
                    key?: string;
                    message?: string;
                    error?: string;
                };
                assertSession();

                if (!response.ok || typeof data.key !== "string" || !data.key.trim()) {
                    throw new Error(data.message || data.error || "TokenDance 授权失败");
                }

                const key = data.key.trim();

                if (state.target === "admin") {
                    if (!state.draft || typeof state.draft !== "object") {
                        throw new Error("后台渠道信息不完整，请重新登录");
                    }

                    sessionStorage.setItem(`tokendance:oauth-result:${flow}`, JSON.stringify({
                        key,
                        ownerId,
                        draft: state.draft,
                        editingChannelIndex: state.editingChannelIndex,
                    }));
                    sessionStorage.removeItem(storageKey);

                    const returnUrl = new URL(safeReturnTo, window.location.origin);
                    returnUrl.searchParams.set("tokendance_oauth", flow);
                    router.replace(`${returnUrl.pathname}${returnUrl.search}${returnUrl.hash}`);
                    return;
                }

                if (!state.channelId || state.channel?.id !== state.channelId || state.channel.protocol !== "tokendance") {
                    throw new Error("对应的 TokenDance 渠道不存在");
                }

                const store = useConfigStore.getState();
                store.switchModelConfigOwner(ownerId);
                const loadVersion = ownerId ? store.beginUserModelConfigLoad(ownerId) : 0;
                const remote = accountToken ? await fetchUserConfig(accountToken) : undefined;
                assertSession();
                if (ownerId) store.applyUserModelConfig(ownerId, loadVersion, remote?.modelConfig);
                const current = useConfigStore.getState();
                if (current.modelConfigOwnerId !== ownerId || ownerId && current.modelConfigLoadVersion !== loadVersion) {
                    throw new Error("渠道配置已变化，请重新登录");
                }
                const channels = normalizeLocalChannels(current.config);
                const target = channels.find((channel) => channel.id === state.channelId);
                if (target?.systemChannelId) throw new Error("对应的 TokenDance 渠道已变化");
                const nextChannel = { ...state.channel, apiKey: key };
                const nextChannels = target
                    ? channels.map((channel) => channel.id === state.channelId ? nextChannel : channel)
                    : [...channels, nextChannel];
                const nextConfig = {
                    ...current.config,
                    localChannels: nextChannels,
                };

                if (accountToken) {
                    const saved = await syncUserModelConfig(accountToken, nextConfig, remote?.modelConfig?.workflowChannels);
                    assertSession();
                    if (useConfigStore.getState().modelConfigLoadVersion !== loadVersion) {
                        throw new Error("渠道配置已变化，请重新登录");
                    }
                    store.applyUserModelConfig(ownerId, loadVersion, saved.modelConfig);
                } else {
                    store.updateConfig("localChannels", nextChannels);
                }

                sessionStorage.removeItem(storageKey);
                message.success("TokenDance 登录成功，API Key 已填入");
                store.openConfigDialog(false);
                router.replace(safeReturnTo);
            } catch (reason) {
                setError(reason instanceof Error ? reason.message : "TokenDance 授权失败");
            }
        })();
    }, [isUserReady, message, router]);

    return (
        <main className="flex h-full items-center justify-center p-6">
            {error ? (
                <Result
                    status="error"
                    title="TokenDance 授权失败"
                    subTitle={error}
                    extra={<Button href="/">返回首页</Button>}
                />
            ) : (
                <Spin size="large" tip="正在完成 TokenDance 授权……" />
            )}
        </main>
    );
}
