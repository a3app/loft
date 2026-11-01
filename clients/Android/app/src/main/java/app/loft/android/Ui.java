/*
 * Android client, shared code (phone and TV builds): small helpers that keep
 * screens smooth. Every screen uses them for the same two jobs: touch a view
 * only when its content really changes, and move slow work (disk, network,
 * parsing, system queries, image work) off the main thread.
 */
package app.loft.android;

import android.app.Activity;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.widget.TextView;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * UI helpers.
 *
 * <p><b>Why "only when changed".</b> Screens redraw from Live up to ten times a
 * second (see Bus). TextView.setText always asks for a new layout pass when
 * the view's width depends on its text, even if the text is the same, and a
 * layout pass measures the whole screen again. Skipping identical updates
 * turns most redraws into almost nothing.
 *
 * <p><b>Why a background pool.</b> The main thread draws the screen; anything
 * slow there (reading a file, asking the system for app icons, decoding a
 * picture, parsing a big list) freezes the UI. {@link #bg} runs such work on
 * a small pool and hands the result back on the main thread.
 */
final class Ui {
    /** Main-thread handler for results. */
    static final Handler MAIN = new Handler(Looper.getMainLooper());
    /**
     * Pool for screen background work. Two threads: enough for a list load
     * and an image job at once without competing with the VPN for the CPU.
     * Daemon threads at background priority so they never hold up drawing.
     */
    private static final ExecutorService BG = Executors.newFixedThreadPool(2, r -> {
        Thread t = new Thread(() -> {
            android.os.Process.setThreadPriority(android.os.Process.THREAD_PRIORITY_BACKGROUND);
            r.run();
        }, "loft-ui-bg");
        t.setDaemon(true);
        return t;
    });

    private Ui() {}

    /** Work that runs in the background and produces a value. */
    interface Job<T> {
        T run() throws Exception;
    }

    /** Receives a background result on the main thread. */
    interface Done<T> {
        void done(T value, Exception error);
    }

    /**
     * Runs {@code job} on the background pool and delivers its result (or
     * error) to {@code done} on the main thread. When {@code owner} is given
     * and has been closed by then (finishing or destroyed), the result is
     * dropped, so a late result never touches a dead screen.
     */
    static <T> void bg(Activity owner, Job<T> job, Done<T> done) {
        BG.execute(() -> {
            T v = null;
            Exception err = null;
            try {
                v = job.run();
            } catch (Exception e) {
                err = e;
            }
            final T value = v;
            final Exception error = err;
            if (done == null) {
                return;
            }
            MAIN.post(() -> {
                if (owner != null && (owner.isFinishing() || owner.isDestroyed())) {
                    return;
                }
                done.done(value, error);
            });
        });
    }

    /** Runs fire-and-forget work on the background pool. */
    static void bg(Runnable r) {
        BG.execute(r);
    }

    /** Sets the text only when it differs from what the view shows (see class comment). */
    static void text(TextView v, CharSequence s) {
        if (v == null) {
            return;
        }
        CharSequence cur = v.getText();
        if (s == null ? cur.length() == 0 : s.toString().contentEquals(cur)) {
            return;
        }
        v.setText(s);
    }

    /** Shows or hides (GONE) a view only when that changes its state. */
    static void shown(View v, boolean show) {
        if (v == null) {
            return;
        }
        int want = show ? View.VISIBLE : View.GONE;
        if (v.getVisibility() != want) {
            v.setVisibility(want);
        }
    }

    /** Sets a view's background drawable resource only when it differs from the last one set here. */
    static void bgRes(View v, int res) {
        if (v == null) {
            return;
        }
        Object tag = v.getTag(R.id.ui_bg_res);
        if (tag instanceof Integer && (Integer) tag == res) {
            return;
        }
        v.setTag(R.id.ui_bg_res, res);
        v.setBackgroundResource(res);
    }
}
