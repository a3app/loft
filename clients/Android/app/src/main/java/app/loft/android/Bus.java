package app.loft.android;

import android.os.Handler;
import android.os.Looper;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * Tiny in-process event bus. The VPN service, the journal and the UI do not
 * hold references to each other; whoever changes shared state (Live,
 * Journal, profiles) calls post(), and every open screen redraws itself on
 * the main thread. The event carries no data: listeners read Live directly.
 */
final class Bus {
    interface Listener {
        void onLoft();
    }

    private static final Handler H = new Handler(Looper.getMainLooper());
    private static final List<Listener> L = new CopyOnWriteArrayList<>();

    static void watch(Listener l) {
        if (l != null) {
            L.add(l);
        }
    }

    static void drop(Listener l) {
        L.remove(l);
    }

    static void post() {
        H.post(() -> {
            for (Listener l : L) {
                l.onLoft();
            }
        });
    }
}
