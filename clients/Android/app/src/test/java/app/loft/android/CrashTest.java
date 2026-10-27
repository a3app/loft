/*
 * Android client unit tests (JVM, no device) for Crash: redaction of keys in
 * a report, a stable stack fingerprint that ignores the exception message,
 * and the GitHub issue title built from the first loft frame.
 */
package app.loft.android;

import org.junit.Test;

import java.io.PrintWriter;
import java.io.StringWriter;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

/** Tests for crash-report redaction, fingerprint and title. */
public final class CrashTest {
    /** Member keys, share URIs, VLESS userinfo, PATs and UUIDs are stripped. */
    @Test
    public void redactSecrets() {
        String raw = "lt_deadbeefcafebabe loft://alice:secret@host/get "
                + "vless://11111111-2222-3333-4444-555555555555@x "
                + "Bearer ghp_abcdefghijklmnopqrstuvwxyz012345 "
                + "id=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
        String got = Crash.redact(raw);
        assertTrue(got.contains("lt_…"));
        assertTrue(got.contains("loft://…@"));
        assertTrue(got.contains("vless://…@"));
        assertTrue(got.contains("Bearer …") || got.contains("ghp_…"));
        assertTrue(got.contains("…uuid…"));
        assertFalse(got.contains("deadbeef"));
        assertFalse(got.contains("secret@"));
        assertFalse(got.contains("11111111-2222"));
        assertFalse(got.contains("ghp_abcdefghijklmnopqrstuvwxyz012345"));
    }

    /** The same frames fingerprint the same way even when the message holds a key. */
    @Test
    public void fingerprintIgnoresMessage() {
        StackTraceElement[] st = new StackTraceElement[] {
                new StackTraceElement("app.loft.android.MainActivity", "onCreate", "MainActivity.java", 42),
                new StackTraceElement("android.app.Activity", "performCreate", "Activity.java", 1),
        };
        NullPointerException a = new NullPointerException("lt_deadbeefdeadbeef");
        a.setStackTrace(st);
        NullPointerException b = new NullPointerException("other");
        b.setStackTrace(st);
        assertEquals(Crash.fingerprint(a), Crash.fingerprint(b));
        assertEquals(8, Crash.fingerprint(a).length());
    }

    /** Title prefers a loft frame over Looper/Thread and never includes the key. */
    @Test
    public void titleUsesLoftFrame() {
        NullPointerException e = new NullPointerException("lt_deadbeefdeadbeef");
        e.setStackTrace(new StackTraceElement[] {
                new StackTraceElement("android.os.Looper", "loop", "Looper.java", 1),
                new StackTraceElement("app.loft.android.MainActivity", "onCreate", "MainActivity.java", 42),
        });
        String t = Crash.title(e);
        assertTrue(t.startsWith("android crash: MainActivity.onCreate ("));
        assertTrue(t.endsWith(")"));
        assertFalse(t.contains("lt_"));
        StringWriter sw = new StringWriter();
        e.printStackTrace(new PrintWriter(sw));
        String red = Crash.redact(sw.toString());
        assertTrue(red.contains("lt_…"));
        assertFalse(red.contains("deadbeef"));
    }

    /** Header fields stop at the blank line; clip adds an ellipsis past the cap. */
    @Test
    public void fieldAndClip() {
        String text = "title: android crash: Foo.bar (abcd1234)\n"
                + "fingerprint: abcd1234\n"
                + "\n"
                + "java.lang.NullPointerException\n";
        assertEquals("android crash: Foo.bar (abcd1234)", Crash.field(text, "title"));
        assertEquals("abcd1234", Crash.field(text, "fingerprint"));
        assertEquals("", Crash.field(text, "missing"));
        assertEquals("ab\n…\n", Crash.clip("abcd", 2));
        assertEquals("abcd", Crash.clip("abcd", 10));
    }
}
