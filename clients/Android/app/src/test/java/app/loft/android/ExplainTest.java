/*
 * Android client unit tests (JVM, no device) for Explain, which turns raw
 * engine and node errors into the short lines shown on Home and in the
 * notification. Checks known cases and that unknown raw dumps never leak.
 */
package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertEquals;

/** Tests for mapping raw error strings to user-facing message resources. */
public final class ExplainTest {
    /**
     * A GET front pointed at a wss:// URL is reported as a wrong-scheme error, not as a raw dump.
     */
    @Test
    public void getWssIsSchemeNotDump() {
        String raw = "timeout: alice-phone: get handshake: Get "
                + "\"wss://api.iqos.help/api/v1/sync/static/v3/build/9b73d01d01db3ac5720706ed/1.js\": "
                + "unsupported protocol scheme \"wss\"";
        assertEquals(R.string.err_scheme_ws, Explain.res(raw));
    }

    /** A handshake timeout while the phone has internet means the node did not answer. */
    @Test
    public void handshakeTimeoutIsNoReply() {
        assertEquals(R.string.err_no_reply,
                Explain.res("timeout: office: get handshake: i/o timeout"));
    }

    /** The same timeout with no network on the phone is reported as "no internet". */
    @Test
    public void noUplink() {
        assertEquals(R.string.err_no_net,
                Explain.res("get handshake: i/o timeout", false));
    }

    /** A 404 during the handshake means the token (key) was not accepted. */
    @Test
    public void auth404() {
        assertEquals(R.string.err_auth_key,
                Explain.res("get handshake: status 404"));
    }

    /** An unrecognised error falls back to the generic message, never the raw text. */
    @Test
    public void unknownDumpNeverLeaks() {
        assertEquals(R.string.err_fail,
                Explain.res("Get \"https://example/x\": something obscure"));
    }

    /** Short error codes sent by the node map to their specific messages. */
    @Test
    public void knownCodesStay() {
        assertEquals(R.string.err_auth_key, Explain.res("auth_bad_key"));
        assertEquals(R.string.err_ip_pool, Explain.res("ip_pool exhausted"));
        assertEquals(R.string.err_stale, Explain.res("echo timeout"));
    }
}
