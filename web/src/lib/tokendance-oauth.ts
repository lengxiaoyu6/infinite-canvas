import { tokenDanceAppUrl } from "@/lib/model-channel";
import { normalizeLocalChannels, useConfigStore, type LocalModelChannel } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

export type TokenDanceOAuthContext =
    | {
        target: "local";
        channelId: string;
    }
    | {
        target: "admin";
        draft: Record<string, unknown>;
        editingChannelIndex: number | null;
    };

export type TokenDanceOAuthState = TokenDanceOAuthContext & {
    ownerId: string;
    channel?: Pick<LocalModelChannel, "id" | "protocol" | "name" | "baseUrl" | "models" | "modelCapabilities">;
    verifier: string;
    returnTo: string;
};

export async function startTokenDanceOAuth(context: TokenDanceOAuthContext) {
    const session = useUserStore.getState();
    if (!session.isReady || session.token && !session.user?.id) throw new Error("登录状态仍在加载");
    const ownerId = session.token ? session.user!.id : "";
    const store = useConfigStore.getState();
    const channel = context.target === "local"
        ? normalizeLocalChannels(store.config).find((item) => item.id === context.channelId && !item.systemChannelId && item.protocol === "tokendance")
        : undefined;
    if (context.target === "local" && (!channel || !store.isModelConfigReady || store.modelConfigOwnerId !== ownerId)) {
        throw new Error("请等待渠道配置加载完成");
    }
    const flow = crypto.randomUUID();
    const verifier = `${crypto.randomUUID().replaceAll("-", "")}${crypto.randomUUID().replaceAll("-", "")}`;
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
    const challenge = btoa(String.fromCharCode(...new Uint8Array(digest)))
        .replace(/\+/g, "-")
        .replace(/\//g, "_")
        .replace(/=+$/, "");
    const callback = new URL("/tokendance/callback", window.location.origin);
    callback.searchParams.set("flow", flow);

    if (useUserStore.getState().token !== session.token || useUserStore.getState().user?.id !== session.user?.id) {
        throw new Error("登录状态已变化，请重新登录");
    }
    sessionStorage.setItem(`tokendance:oauth:${flow}`, JSON.stringify({
        ...context,
        ownerId,
        ...(channel ? { channel: { id: channel.id, protocol: channel.protocol, name: channel.name, baseUrl: channel.baseUrl, models: channel.models, modelCapabilities: channel.modelCapabilities } } : {}),
        verifier,
        returnTo: `${window.location.pathname}${window.location.search}${window.location.hash}`,
    }));

    const authUrl = new URL("https://tokendance.space/auth");
    authUrl.searchParams.set("callback_url", callback.toString());
    authUrl.searchParams.set("code_challenge", challenge);
    authUrl.searchParams.set("code_challenge_method", "S256");
    authUrl.searchParams.set("app_url", tokenDanceAppUrl);
    authUrl.searchParams.set("key_name", "Infinite Canvas");
    window.location.assign(authUrl);
}
