/*
 * Android client unit tests (JVM, no device) for Ver (release version ordering,
 * including pre-releases) and Sums (reading one file's hash from a SHA256SUMS
 * list). Both are used by the GitHub self-updater in Channel.java.
 */
package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;

/** Tests for version comparison and SHA256SUMS lookup used by the updater. */
public final class VerTest {
    /**
     * Versions compare numerically per part; a pre-release sorts before its release, and
     * pre-release labels compare as text.
     */
    @Test
    public void order() {
        assertEquals(-1, Ver.cmp("1.0.27", "1.0.28"));
        assertEquals(1, Ver.cmp("1.0.28", "1.0.27"));
        assertEquals(0, Ver.cmp("1.0.27", "1.0.27"));
        assertEquals(1, Ver.cmp("1.1.0", "1.0.99"));
        assertEquals(-1, Ver.cmp("1.0.27-pre", "1.0.27"));
        assertEquals(1, Ver.cmp("1.0.27", "1.0.27-pre"));
        assertEquals(-1, Ver.cmp("1.0.27-a", "1.0.27-b"));
    }

    /** Release-candidate suffixes like -rc.1 sort before the plain release. */
    @Test
    public void tagShape() {
        assertEquals(0, Ver.cmp("1.0.27", "1.0.27"));
        assertEquals(1, Ver.cmp("1.0.27", "1.0.27-rc.1"));
    }

    /**
     * hexFor finds a file's hash in text or binary (*name) form, skips comments, and returns null
     * for a missing file.
     */
    @Test
    public void sums() {
        String body = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  loft-android.apk\n"
                + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb *loft-android-tv.apk\n"
                + "# comment\n";
        assertEquals("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                Sums.hexFor(body, "loft-android.apk"));
        assertEquals("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                Sums.hexFor(body, "loft-android-tv.apk"));
        assertNull(Sums.hexFor(body, "missing.apk"));
    }
}
