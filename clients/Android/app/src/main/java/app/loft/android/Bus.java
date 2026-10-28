/*
 * Android client, shared code (phone and TV builds): the in-process event
 * bus. The VPN service (LoftVpnService), the log (Journal) and the home
 * screens (after the public-address lookup) post here after changing state;
 * screens, the quick-settings tile and the Lists screen subscribe while they
 * are visible and redraw from Live.
 */
package app.loft.android;

import android.os.Handler;
import android.os.Looper;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * Tiny in-process event bus. The VPN service, the journal and the UI do not
 * hold references to each other; whoever changes shared state (Live,
 * Journal) calls post(), and every open screen redraws itself on the main
 * thread. The event carries no data: listeners read Live directly.
 *
 * <p>Every post() is delivered separately: there is no merging of events
 * that arrive close together, so a burst of posts means a burst of redraws.
 * The VPN service posts at least once a second while a session runs.
 */
final class Bus {
    /** Implemented by screens that want to redraw when shared state changes. */
    interface Listener {
        /** Called on the main thread after any post(). */
        void onLoft();
    }

    /** Main-thread handler, so listeners can touch views directly. */
    private static final Handler H = new Handler(Looper.getMainLooper());
    /** Current subscribers; copy-on-write so post() can iterate while screens subscribe or leave. */
    private static final List<Listener> L = new CopyOnWriteArrayList<>();

    /** Subscribes a listener (usually in onResume). Null is ignored. */
    static void watch(Listener l) {
        if (l != null) {
            L.add(l);
        }
    }

    /** Unsubscribes a listener (usually in onPause) so a hidden screen is not redrawn or leaked. */
    static void drop(Listener l) {
        L.remove(l);
    }

    /**
     * Tells every subscriber, on the main thread, that shared state changed.
     * Safe to call from any thread: the work is queued on the main Looper, so
     * the caller never waits for the redraw.
     */
    static void post() {
        H.post(() -> {
            for (Listener l : L) {
                l.onLoft();
            }
        });
    }
}
