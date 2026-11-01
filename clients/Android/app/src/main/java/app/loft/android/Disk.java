/*
 * Android client, shared code (phone and TV builds): safe, off-main-thread
 * file writes. Profiles (the saved connections) and Doc (the big route list
 * documents) save through here.
 */
package app.loft.android;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.Callable;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

/**
 * Atomic file writes on one background thread, "loft-disk".
 *
 * <p><b>Atomic:</b> a write goes to "name.tmp", is flushed to the storage
 * chip (fsync), and is then renamed over the real file. A rename within one
 * directory is atomic on Linux, so after a crash, a kill or a power loss the
 * file holds either the old content or the new one, never half of each. A
 * plain overwrite, as before, could leave a truncated file and lose every
 * saved connection.
 *
 * <p><b>Off the main thread and merged:</b> {@link #writeLater} returns at
 * once. The content is produced on loft-disk by the caller's {@code make}
 * function (so slow work such as encryption also happens there), and if the
 * same file is saved again before the write ran, only the newest version is
 * written. Ten quick edits cost one write.
 */
final class Disk {
    /** The single writer thread; daemon so it never keeps the process alive. */
    private static final ExecutorService W = Executors.newSingleThreadExecutor(r -> {
        Thread t = new Thread(r, "loft-disk");
        t.setDaemon(true);
        return t;
    });
    /** Newest pending content maker per file; a file is in the map while its write is queued. */
    private static final Map<File, Callable<byte[]>> PENDING = new HashMap<>();

    private Disk() {}

    /**
     * Queues a write of {@code f}. {@code make} runs later on loft-disk and
     * returns the bytes to write (or null to skip). A newer call for the same
     * file before the write runs replaces {@code make}.
     */
    static void writeLater(File f, Callable<byte[]> make) {
        boolean schedule;
        synchronized (PENDING) {
            schedule = PENDING.put(f, make) == null;
        }
        if (schedule) {
            W.execute(() -> {
                Callable<byte[]> m;
                synchronized (PENDING) {
                    m = PENDING.remove(f);
                }
                if (m == null) {
                    return;
                }
                try {
                    byte[] b = m.call();
                    if (b != null) {
                        writeNow(f, b);
                    }
                } catch (Exception e) {
                    Journal.add("save " + f.getName() + " " + e.getMessage());
                }
            });
        }
    }

    /**
     * Waits (up to {@code ms}) until every queued write is on disk. For the
     * rare moments that must not lose a save, such as right before the
     * process is replaced by an update; never called on the main thread.
     */
    static void flush(long ms) {
        try {
            W.submit(() -> { }).get(ms, TimeUnit.MILLISECONDS);
        } catch (Exception ignored) {
        }
    }

    /** Writes {@code b} to {@code f} atomically (tmp file, fsync, rename), on the calling thread. */
    static void writeNow(File f, byte[] b) throws IOException {
        File dir = f.getParentFile();
        if (dir != null && !dir.isDirectory() && !dir.mkdirs()) {
            throw new IOException("no dir " + dir);
        }
        File tmp = new File(f.getPath() + ".tmp");
        try (FileOutputStream out = new FileOutputStream(tmp)) {
            out.write(b);
            out.flush();
            // fsync: make sure the bytes are on the storage chip before the
            // rename makes them the real file.
            out.getFD().sync();
        }
        if (!tmp.renameTo(f)) {
            tmp.delete();
            throw new IOException("rename " + tmp.getName());
        }
    }

    /** Reads a whole file; null when it does not exist. */
    static byte[] read(File f) throws IOException {
        if (!f.isFile()) {
            return null;
        }
        try (InputStream in = new FileInputStream(f)) {
            ByteArrayOutputStream out = new ByteArrayOutputStream((int) Math.max(32, f.length()));
            byte[] buf = new byte[16 * 1024];
            int n;
            while ((n = in.read(buf)) > 0) {
                out.write(buf, 0, n);
            }
            return out.toByteArray();
        }
    }
}
