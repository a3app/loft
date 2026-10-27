/*
 * Explain: turns raw error text from the Go engine or the node (the loft
 * server the app connects to) into one short translated line for the Home
 * screen and the notification. The full error stays in the journal
 * (Journal / LogActivity). Covered by ExplainTest.
 */
package app.loft.android;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;

/**
 * Maps engine/node errors the way macOS FrontExplain does. Home and the
 * notification show only these short lines; the journal keeps the raw dump.
 */
final class Explain {
    /**
     * Translated human line for a raw engine or node error. Unknown dumps
     * (URLs, Go wrappers) become a generic connect failure, never the raw text.
     */
    static String display(Context ctx, String raw) {
        if (raw == null || raw.isEmpty()) {
            return "";
        }
        return ctx.getString(res(raw, uplink(ctx)));
    }

    /** Resource for tests. Assumes a network is up. */
    static int res(String raw) {
        return res(raw, true);
    }

    /**
     * Picks the string resource for a raw error by looking for known markers,
     * most specific first. {@code uplink} says whether the phone has any
     * internet; without it, network-level failures are reported as "no
     * network" instead of blaming the node.
     */
    static int res(String raw, boolean uplink) {
        if (raw == null || raw.isEmpty()) {
            return R.string.err_fail;
        }
        String low = raw.toLowerCase();
        // 1. Codes the node sends when it refuses this member (auth_*), and HTTP-level rejections.
        if (low.contains("auth_limit")) {
            return R.string.err_auth_limit;
        }
        if (low.contains("auth_already_online")) {
            return R.string.err_auth_online;
        }
        if (low.contains("auth_bad_key") || low.contains("auth_revoked")
                || (low.contains("get handshake") && low.contains("404"))) {
            return R.string.err_auth_key;
        }
        if (low.contains("auth_not_authorized")) {
            return R.string.err_auth_denied;
        }
        if (low.contains("auth_expired")) {
            return R.string.err_auth_expired;
        }
        if (low.contains("header too large") || low.contains("status 413")) {
            return R.string.err_get_413;
        }
        if (low.contains("method not allowed") || low.contains("status 405")) {
            return R.string.err_get_405;
        }
        if (low.contains("requires mux")) {
            return R.string.err_need_mux;
        }
        // 2. Session and config problems (network switched off, address pool full, empty config...).
        if (low.contains("net_disabled")) {
            return R.string.err_net_off;
        }
        if (low.contains("ip_pool")) {
            return R.string.err_ip_pool;
        }
        if (low.contains("policy_drop")) {
            return R.string.err_policy;
        }
        if (low.contains("echo timeout")) {
            return R.string.err_stale;
        }
        if (low.contains("no overlay")) {
            return R.string.err_no_overlay;
        }
        if (low.contains("empty yaml") || low.contains("empty config")) {
            return R.string.err_empty;
        }
        if (low.contains("no connection")) {
            return R.string.err_no_conn;
        }
        // 3. Transport and protocol specific errors (WebSocket, front, VLESS, Trojan, SSTP).
        // GET Auto against a ws:// / wss:// URL.
        if (low.contains("unsupported protocol scheme")) {
            if (low.contains("wss") || low.contains("\"ws\"")) {
                return R.string.err_scheme_ws;
            }
            return R.string.err_scheme;
        }
        if (low.contains("wss dial")) {
            return R.string.err_ws;
        }
        if (low.contains("front closed")) {
            return R.string.err_front_closed;
        }
        if (low.contains("reality: dest") || low.contains("reality dest")
                || low.contains("vless reality")) {
            return R.string.err_reality;
        }
        if (low.contains("vless uuid")) {
            return R.string.err_vless_uuid;
        }
        if (low.contains("vless transport")) {
            return R.string.err_vless_transport;
        }
        if (low.startsWith("vless:") || low.startsWith("vless ")) {
            return R.string.err_vless;
        }
        if (low.contains("trojan password")) {
            return R.string.err_trojan_password;
        }
        if (low.contains("trojan transport")) {
            return R.string.err_trojan_transport;
        }
        if (low.startsWith("trojan:") || low.startsWith("trojan ")) {
            return R.string.err_trojan;
        }
        if (low.contains("chap failure") || low.contains("chap authenticator")
                || low.contains("pap rejected")) {
            return R.string.err_sstp_chap;
        }
        if (low.contains("sstp http")) {
            return R.string.err_sstp_http;
        }
        if (low.startsWith("sstp ") && !low.startsWith("sstp up") && !low.startsWith("sstp: ")) {
            return R.string.err_sstp;
        }
        // 4. Plain network failures: refine by cause, or say "no network" when offline.
        boolean getHs = low.contains("get handshake");
        if (transport(low) || (getHs && !low.contains("empty session") && !low.contains("status"))) {
            if (!uplink) {
                return R.string.err_no_net;
            }
            if (noReply(low) || getHs) {
                return R.string.err_no_reply;
            }
            if (low.contains("connection refused")) {
                return R.string.err_refused;
            }
            if (low.contains("connection reset")) {
                return R.string.err_reset;
            }
            if (low.contains("no such host") || low.contains("nodename nor servname")) {
                return R.string.err_dns;
            }
            if (low.contains("network is unreachable") || low.contains("no route")) {
                return R.string.err_unreach;
            }
            return R.string.err_no_reply;
        }
        // 5. HTTP GET handshake failures that are not network errors, then short bare words.
        if (getHs) {
            if (low.contains("empty session")) {
                return R.string.err_get_empty;
            }
            if (low.contains("status")) {
                return R.string.err_get_status;
            }
            return R.string.err_get_hs;
        }
        if (low.equals("ws") || low.startsWith("ws ")) {
            return R.string.err_ws;
        }
        if (low.equals("eof") || low.endsWith(" eof")) {
            return R.string.err_eof;
        }
        if (low.equals("front") || low.startsWith("front ")) {
            return R.string.err_front;
        }
        return R.string.err_fail;
    }

    /** True for timeout-style errors: the other side never answered. */
    private static boolean noReply(String low) {
        return low.contains("i/o timeout")
                || low.contains("timed out")
                || low.contains("timeout")
                || low.contains("deadline exceeded")
                || low.contains("no response");
    }

    /** True for errors from the network layer (timeouts, refused, reset, DNS, no route). */
    private static boolean transport(String low) {
        return noReply(low)
                || low.contains("connection refused")
                || low.contains("connection reset")
                || low.contains("network is unreachable")
                || low.contains("no route to host")
                || low.contains("network is down")
                || low.contains("no such host")
                || low.contains("nodename nor servname")
                || low.contains("host is down")
                || low.contains("broken pipe")
                || low.contains("connection failed");
    }

    /**
     * True when the active network claims internet access. Errors and a missing
     * ConnectivityManager count as "up", so the message never wrongly blames
     * the phone's connection.
     */
    private static boolean uplink(Context ctx) {
        try {
            ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
            if (cm == null) {
                return true;
            }
            Network n = cm.getActiveNetwork();
            if (n == null) {
                return false;
            }
            NetworkCapabilities c = cm.getNetworkCapabilities(n);
            return c != null && c.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET);
        } catch (Exception e) {
            return true;
        }
    }
}
