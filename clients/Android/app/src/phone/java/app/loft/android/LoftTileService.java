/*
 * Android client, phone build: the Quick Settings tile (the button in the
 * pull-down shade). It starts and stops LoftVpnService and repaints itself
 * from Live state whenever the Bus (in-process event bus) fires.
 * The system binds to this service only while the shade is open, so the
 * tile listens to the Bus only in that window.
 */
package app.loft.android;

import android.content.Intent;
import android.net.VpnService;
import android.os.Build;
import android.service.quicksettings.Tile;
import android.service.quicksettings.TileService;

/**
 * Quick Settings tile: one tap connects or disconnects the active profile
 * (the saved connection marked as current in Profiles). Opens the app when
 * there is no profile yet, and the system VPN consent dialog when permission
 * has not been granted. All callbacks run on the main thread.
 */
public final class LoftTileService extends TileService implements Bus.Listener {
    /** Called when the shade opens: subscribe to state changes and paint the current state. */
    @Override
    public void onStartListening() {
        Bus.watch(this);
        // force: the system may have rebuilt the tile while the shade was closed
        // (for example after SystemUI restarted), so always push our state once here.
        paint(true);
    }

    /** Called when the shade closes: stop listening, nothing to repaint. */
    @Override
    public void onStopListening() {
        Bus.drop(this);
    }

    /** Bus callback: VPN state changed, repaint the tile. */
    @Override
    public void onLoft() {
        paint(false);
    }

    /**
     * Tap handler. Stops the tunnel if it is running or connecting; otherwise opens the app
     * when there is no profile, asks for VPN consent when needed, or starts the tunnel.
     * startActivityAndCollapse needs a PendingIntent on API 34+; a plain Intent is refused there
     * (it throws UnsupportedOperationException for apps targeting 34 or newer), so both places
     * that open a screen go through {@link #open}.
     */
    @Override
    public void onClick() {
        // 1. Already up or on its way up: the tap means "turn it off".
        if (LoftVpnService.running || Live.on() || Live.connecting()) {
            LoftVpnService.requestStop(this);
            return;
        }
        // 2. Nothing to connect to yet: open the app so the user can add a profile,
        //    and collapse the shade so the app is visible.
        if (LoftApp.of(this).profiles().active() == null) {
            Intent i = new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            open(i, 0);
            return;
        }
        // 3. VpnService.prepare returns an Intent for the system consent dialog the first
        //    time (or after another VPN app took over); null means consent is already given.
        //    Like step 2 it goes through open(), so API 34+ gets the PendingIntent it requires.
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            prep.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            open(prep, 1);
            return;
        }
        // 4. Everything is in place: ask the service to connect. The tile repaints when
        //    the service posts the new state on the Bus.
        LoftVpnService.requestStart(this);
    }

    /**
     * Opens a screen from the tile and collapses the shade so the screen is visible.
     * On API 34+ the Intent must be wrapped in a PendingIntent: the plain-Intent
     * overload throws there. FLAG_IMMUTABLE is required for PendingIntents since API 31
     * and is right here because nobody needs to fill in the Intent later.
     * {@code code} is the PendingIntent request code; each caller uses its own so the
     * system never mixes up the "open app" and "VPN consent" PendingIntents.
     */
    private void open(Intent i, int code) {
        if (Build.VERSION.SDK_INT >= 34) {
            startActivityAndCollapse(android.app.PendingIntent.getActivity(this, code, i,
                    android.app.PendingIntent.FLAG_IMMUTABLE | android.app.PendingIntent.FLAG_UPDATE_CURRENT));
        } else {
            startActivityAndCollapse(i);
        }
    }

    /**
     * Shows the tile as active while connected or connecting, inactive otherwise.
     * getQsTile() is null when the system has not bound the tile yet.
     *
     * <p>updateTile() is a call into the system UI process. The Bus fires up to ten
     * times a second while the VPN is busy, but the tile only has two looks, so it
     * is pushed only when the label or state really differs from what the tile
     * already shows (or when {@code force} is set). On slow phones (HyperOS) the
     * stream of identical updates made the shade itself stutter.
     */
    private void paint(boolean force) {
        Tile t = getQsTile();
        if (t == null) {
            return;
        }
        String label = "loft";
        int state = Live.on() || Live.connecting() ? Tile.STATE_ACTIVE : Tile.STATE_INACTIVE;
        if (!force && t.getState() == state && label.contentEquals(String.valueOf(t.getLabel()))) {
            return;
        }
        t.setLabel(label);
        t.setState(state);
        t.updateTile();
    }
}
