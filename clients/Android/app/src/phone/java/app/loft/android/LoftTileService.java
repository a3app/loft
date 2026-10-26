package app.loft.android;

import android.content.Intent;
import android.net.VpnService;
import android.os.Build;
import android.service.quicksettings.Tile;
import android.service.quicksettings.TileService;

/**
 * Quick Settings tile: one tap connects or disconnects the active profile.
 * Opens the app when there is no profile yet, and the system VPN consent
 * dialog when permission has not been granted.
 */
public final class LoftTileService extends TileService implements Bus.Listener {
    @Override
    public void onStartListening() {
        Bus.watch(this);
        paint();
    }

    @Override
    public void onStopListening() {
        Bus.drop(this);
    }

    @Override
    public void onLoft() {
        paint();
    }

    // startActivityAndCollapse needs a PendingIntent on API 34+; a plain
    // Intent is refused there.
    @Override
    public void onClick() {
        if (LoftVpnService.running || Live.on() || Live.connecting()) {
            LoftVpnService.requestStop(this);
            return;
        }
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
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            prep.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            startActivityAndCollapse(prep);
            return;
        }
        LoftVpnService.requestStart(this);
    }

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
