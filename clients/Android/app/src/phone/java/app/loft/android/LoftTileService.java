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
        paint();
    }

    /** Called when the shade closes: stop listening, nothing to repaint. */
    @Override
    public void onStopListening() {
        Bus.drop(this);
    }

    /** Bus callback: VPN state changed, repaint the tile. */
    @Override
    public void onLoft() {
        paint();
    }

    /**
     * Tap handler. Stops the tunnel if it is running or connecting; otherwise opens the app
     * when there is no profile, asks for VPN consent when needed, or starts the tunnel.
     * startActivityAndCollapse needs a PendingIntent on API 34+; a plain Intent is refused there.
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
            if (Build.VERSION.SDK_INT >= 34) {
                startActivityAndCollapse(android.app.PendingIntent.getActivity(
                        this, 0, i, android.app.PendingIntent.FLAG_IMMUTABLE));
            } else {
                startActivityAndCollapse(i);
            }
            return;
        }
        // 3. VpnService.prepare returns an Intent for the system consent dialog the first
        //    time (or after another VPN app took over); null means consent is already given.
        //    Note: unlike step 2, this always passes a plain Intent, which API 34+ refuses.
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            prep.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            startActivityAndCollapse(prep);
            return;
        }
        // 4. Everything is in place: ask the service to connect. The tile repaints when
        //    the service posts the new state on the Bus.
        LoftVpnService.requestStart(this);
    }

    /**
     * Shows the tile as active while connected or connecting, inactive otherwise.
     * getQsTile() is null when the system has not bound the tile yet. updateTile()
     * is a call into the system UI process, made on every Bus event while the shade is open.
     */
    private void paint() {
        Tile t = getQsTile();
        if (t == null) {
            return;
        }
        t.setLabel("loft");
        t.setState(Live.on() || Live.connecting() ? Tile.STATE_ACTIVE : Tile.STATE_INACTIVE);
        t.updateTile();
    }
}
