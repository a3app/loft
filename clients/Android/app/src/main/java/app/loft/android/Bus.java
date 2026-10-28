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
import android.os.SystemClock;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Tiny in-process event bus. The VPN service, the journal and the UI do not
 * hold references to each other; whoever changes shared state (Live,
 * Journal) calls post(), and every open screen redraws itself on the main
 * thread. The event carries no data: listeners read Live directly.
 *
 * <p>Posts are merged. Because the event carries no data, ten posts in a row
 * mean exactly what one does: "look at Live again". So while one delivery is
 * queued, further posts are dropped, and deliveries are spaced at least
 * {@link #MIN_GAP_MS} apart. A burst of log lines or a reconnect storm then
 * costs a few redraws instead of hundreds, which is what kept slower phones
 * (and HyperOS in particular) from keeping up.
 */
final class Bus {
    /** Implemented by screens that want to redraw when shared state changes. */
    interface Listener {
        /** Called on the main thread after one or more post() calls. */
        void onLoft();
    }

    /** Shortest time between two deliveries: at most 10 redraws a second. */
    static final long MIN_GAP_MS = 100;

    /** Main-thread handler, so listeners can touch views directly. */
    private static final Handler H = new Handler(Looper.getMainLooper());
    /** Current subscribers; copy-on-write so delivery can iterate while screens subscribe or leave. */
    private static final List<Listener> L = new CopyOnWriteArrayList<>();
    /** True while a delivery is queued on the main thread; further posts ride along with it. */
    private static final AtomicBoolean QUEUED = new AtomicBoolean();
    /** Uptime (ms) of the last delivery, for the spacing above. */
    private static volatile long lastRun;

    /**
     * One delivery: clears the queued flag first, so a post made by a listener
     * (or by another thread) during delivery queues the next one rather than
     * being lost.
     */
    private static final Runnable DELIVER = () -> {
        QUEUED.set(false);
        lastRun = SystemClock.uptimeMillis();
        for (Listener l : L) {
            l.onLoft();
        }
    };

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
     * Safe and cheap to call from any thread, as often as you like: at most
     * one delivery is ever queued.
     */
    static void post() {
        if (!QUEUED.compareAndSet(false, true)) {
            return;
        }
        long wait = lastRun + MIN_GAP_MS - SystemClock.uptimeMillis();
        H.postDelayed(DELIVER, Math.max(0, wait));
    }
}
