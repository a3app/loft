/*
 * Android client, unit tests (JVM, no device): Disk, the atomic background
 * file writer behind Profiles and Doc.
 */
package app.loft.android;

import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;

import java.io.File;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;

/** Tests for atomic writes and merged background writes. */
public class DiskTest {
    @Rule
    public TemporaryFolder tmp = new TemporaryFolder();

    /** writeNow replaces the content in one step and leaves no temp file behind. */
    @Test
    public void writeNowReplacesAtomically() throws Exception {
        File f = new File(tmp.getRoot(), "sub/a.json");
        Disk.writeNow(f, "one".getBytes(StandardCharsets.UTF_8));
        Disk.writeNow(f, "two".getBytes(StandardCharsets.UTF_8));
        assertEquals("two", new String(Disk.read(f), StandardCharsets.UTF_8));
        assertFalse(new File(f.getPath() + ".tmp").exists());
        assertNull(Disk.read(new File(tmp.getRoot(), "missing")));
    }

    /**
     * Saves queued while the writer is busy collapse into one write of the
     * newest content: a burst of edits costs one disk write.
     */
    @Test
    public void writeLaterMergesBursts() throws Exception {
        File f = new File(tmp.getRoot(), "b.json");
        File gate = new File(tmp.getRoot(), "gate");
        AtomicInteger made = new AtomicInteger();
        // Hold the writer thread on another file so the next saves queue up.
        Object lock = new Object();
        synchronized (lock) {
            Disk.writeLater(gate, () -> {
                synchronized (lock) {
                    return new byte[0];
                }
            });
            for (int i = 0; i < 10; i++) {
                final int v = i;
                Disk.writeLater(f, () -> {
                    made.incrementAndGet();
                    return ("v" + v).getBytes(StandardCharsets.UTF_8);
                });
            }
        }
        Disk.flush(5000);
        assertEquals("v9", new String(Disk.read(f), StandardCharsets.UTF_8));
        assertEquals(1, made.get());
    }
}
