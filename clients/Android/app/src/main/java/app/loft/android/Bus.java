package app.loft.android;

import android.os.Handler;
import android.os.Looper;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

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
