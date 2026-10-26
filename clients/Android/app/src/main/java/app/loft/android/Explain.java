package app.loft.android;

import android.content.Context;

/** Maps engine/node errors the way macOS FrontExplain does. */
final class Explain {
    static String display(Context ctx, String raw) {
        if (raw == null || raw.isEmpty()) {
            return "";
        }
        String low = raw.toLowerCase();
        int id = 0;
        if (low.contains("auth_limit")) {
            id = R.string.err_auth_limit;
        } else if (low.contains("auth_already_online")) {
            id = R.string.err_auth_online;
        } else if (low.contains("auth_bad_key") || low.contains("auth_revoked")
                || (low.contains("get handshake") && low.contains("404"))) {
            id = R.string.err_auth_key;
        } else if (low.contains("auth_not_authorized")) {
            id = R.string.err_auth_denied;
        } else if (low.contains("auth_expired")) {
            id = R.string.err_auth_expired;
        } else if (low.contains("header too large") || low.contains("status 413")) {
            id = R.string.err_get_413;
        } else if (low.contains("method not allowed") || low.contains("status 405")) {
            id = R.string.err_get_405;
        } else if (low.contains("requires mux")) {
            id = R.string.err_need_mux;
        } else if (low.contains("net_disabled")) {
            id = R.string.err_net_off;
        } else if (low.contains("ip_pool")) {
            id = R.string.err_ip_pool;
        } else if (low.contains("policy_drop")) {
            id = R.string.err_policy;
        } else if (low.contains("echo timeout")) {
            id = R.string.err_stale;
        }
        return id == 0 ? raw : ctx.getString(id);
    }
}
