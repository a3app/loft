/*
 * Android client unit tests (JVM, no device) for Explain, which turns raw
 * engine and node errors into the short lines shown on Home and in the
 * notification. Checks known cases and that unknown raw dumps never leak.
 */
package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertEquals;

public final class ExplainTest {
    @Test
    public void getWssIsSchemeNotDump() {
        String raw = "timeout: alice-phone: get handshake: Get "
                + "\"wss://api.iqos.help/api/v1/sync/static/v3/build/9b73d01d01db3ac5720706ed/1.js\": "
                + "unsupported protocol scheme \"wss\"";
        assertEquals(R.string.err_scheme_ws, Explain.res(raw));
    }

    @Test
    public void handshakeTimeoutIsNoReply() {
        assertEquals(R.string.err_no_reply,
                Explain.res("timeout: office: get handshake: i/o timeout"));
    }

    @Test
    public void noUplink() {
        assertEquals(R.string.err_no_net,
                Explain.res("get handshake: i/o timeout", false));
    }

    @Test
    public void auth404() {
        assertEquals(R.string.err_auth_key,
                Explain.res("get handshake: status 404"));
    }

    @Test
    public void unknownDumpNeverLeaks() {
        assertEquals(R.string.err_fail,
                Explain.res("Get \"https://example/x\": something obscure"));
    }

    @Test
    public void knownCodesStay() {
        assertEquals(R.string.err_auth_key, Explain.res("auth_bad_key"));
        assertEquals(R.string.err_ip_pool, Explain.res("ip_pool exhausted"));
        assertEquals(R.string.err_stale, Explain.res("echo timeout"));
    }
}
