/*
 * Android client, phone build: the main screen with the Home, Connections and
 * Settings tabs, and the entry point for imports (loft:// links, shared text,
 * opened files). It starts and stops LoftVpnService and redraws from Live
 * on Bus events; it never talks to the engine directly. Other phone screens
 * (EditActivity, ScanActivity, ListsActivity, AppsActivity) open from here.
 * The TV build uses TvActivity instead.
 */
package app.loft.android;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ClipboardManager;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.net.VpnService;
import android.os.Build;
import android.os.Bundle;
import android.provider.Settings;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.CompoundButton;
import android.widget.EditText;
import android.widget.ImageView;
import android.widget.Spinner;
import android.widget.TextView;
import android.widget.Toast;

import androidx.annotation.NonNull;
import androidx.recyclerview.widget.LinearLayoutManager;
import androidx.recyclerview.widget.RecyclerView;

import com.google.android.material.bottomnavigation.BottomNavigationView;
import com.google.android.material.bottomsheet.BottomSheetDialog;
import com.google.android.material.floatingactionbutton.FloatingActionButton;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Phone main screen with three tabs: Home (big connect button, status,
 * traffic chart, addresses), Connections (saved profiles) and Settings.
 * The tabs are views swapped inside one activity. Also the entry point for
 * imports: loft:// links, shared text and opened files arrive here.
 * The screen never talks to the engine; it starts and stops LoftVpnService
 * and redraws from Live on every Bus event.
 */
public final class MainActivity extends LoftActivity implements Bus.Listener {
    /** The bottom tab bar pads itself for the navigation bar (see LoftActivity). */
    @Override
    protected boolean bottomBarHandlesInsets() {
        return true;
    }

    /** Request codes for the VPN consent dialog and the notification permission prompt. */
    private static final int REQ_VPN = 1;
    private static final int REQ_NOTE = 2;

    /** Container that holds the three tab views; only one is visible at a time. */
    private ViewGroup pane;
    /** Home tab view (pane_home): connect button, status pill, cards, chart, addresses. Always created. */
    private View home;
    /** Connections tab view (pane_conns); null until ensureConns() creates it. */
    private View conns;
    /** Settings tab view (pane_settings); null until ensureSettings() creates it. */
    private View settings;
    /** Adapter of the Connections list; null until the Connections tab exists. */
    private ConnAdapter adapter;
    /**
     * True once the notification permission was requested in this screen's life, so it is
     * asked at most once. (The name is historical: it guards the notification prompt, not
     * the VPN consent dialog.)
     */
    private boolean askedVpn;
    /** True while a public-IP lookup is running, so taps and redraws do not start a second one. */
    private final AtomicBoolean extFetch = new AtomicBoolean();
    /** Failed public-IP lookups in a row; sets the retry delay and stops retrying after a few. */
    private int extTries;
    /** The queued public-IP retry, kept so onPause can cancel it. */
    private final Runnable extRetry = this::retryExt;
    /** True while extRetry is queued (Handler.hasCallbacks needs API 29). */
    private boolean extQueued;
    /** Views on Home, looked up once (see HomeViews). */
    private HomeViews hv;
    /**
     * What Home and the connection list last showed, so a redraw that would
     * change nothing is skipped (see paintHome and onLoft). -1 / null mean
     * "not drawn yet". errShown/errText cache the explained error text.
     */
    private int goState = -1;
    private String listSig;
    private String errShown;
    private String errText = "";

    /**
     * Shows Home first, wires the bottom tabs and the Home buttons, handles a link or file the app
     * was opened with, opens onboarding when there are no profiles yet, and auto-connects when
     * that setting is on.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        // The manifest starts this screen with Theme.Loft.Splash (splash picture as the window
        // background, shown while the process starts). Switch to the normal theme before any
        // view is inflated so the real screen does not keep the splash background.
        setTheme(R.style.Theme_Loft);
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);
        pane = findViewById(R.id.pane);
        home = getLayoutInflater().inflate(R.layout.pane_home, pane, false);
        pane.addView(home);
        hv = new HomeViews(home);
        show(home);

        // 1. Bottom tabs: the Connections and Settings views are created on first use.
        BottomNavigationView nav = findViewById(R.id.nav);
        nav.setOnItemSelectedListener(item -> {
            int id = item.getItemId();
            if (id == R.id.nav_home) {
                show(home);
            } else if (id == R.id.nav_conns) {
                ensureConns();
                show(conns);
            } else {
                ensureSettings();
                show(settings);
            }
            return true;
        });

        // 2. Home: big button connects or disconnects, the pill opens Connections, cards open details.
        home.findViewById(R.id.go).setOnClickListener(v -> toggle());
        home.findViewById(R.id.pill).setOnClickListener(v -> nav.setSelectedItemId(R.id.nav_conns));
        home.findViewById(R.id.cards).setOnClickListener(v -> details());

        // 3. Import whatever started the app, then onboarding or auto-connect.
        // Onboarding opens only when there are no profiles AND the user has not finished or
        // skipped it before ("onboard_done"), so deleting every profile later does not
        // bring the wizard back. Auto-connect is skipped when a session is already running
        // (e.g. the screen was recreated while the VPN was up).
        // The import runs only on a fresh start: when Android recreates the
        // screen (rotation, dark mode, language) or restores it after the
        // process was killed, getIntent() is the same old intent, and
        // importing it again would add the same profiles a second time. An
        // import finishes in the background, so while one is on its way the
        // empty profile list does not mean "first run".
        boolean importing = savedInstanceState == null && handleIntent(getIntent());
        if (!importing && LoftApp.of(this).profiles().all().isEmpty()
                && !Prefs.p(this).getBoolean("onboard_done", false)) {
            startActivity(new Intent(this, OnboardActivity.class));
        }
        if (Prefs.autoConnect(this) && LoftApp.of(this).profiles().active() != null
                && !LoftVpnService.running && !Live.on()) {
            connect();
        }
        // Connections/Settings are heavy; inflate after the first Home frame so
        // the emulator does not ANR ("system isn't responding") on cold start.
        pane.post(() -> {
            ensureConns();
            ensureSettings();
        });
    }

    /** Creates the Connections tab on first use: the profile list and the add button. */
    private void ensureConns() {
        if (conns != null) {
            return;
        }
        conns = getLayoutInflater().inflate(R.layout.pane_conns, pane, false);
        conns.setVisibility(View.GONE);
        pane.addView(conns);
        adapter = new ConnAdapter();
        RecyclerView list = conns.findViewById(R.id.list);
        list.setLayoutManager(new LinearLayoutManager(this));
        list.setAdapter(adapter);
        conns.findViewById(R.id.fab).setOnClickListener(v -> addSheet());
        adapter.reload();
    }

    /** Creates the Settings tab on first use and binds it to Prefs. */
    private void ensureSettings() {
        if (settings != null) {
            return;
        }
        settings = getLayoutInflater().inflate(R.layout.pane_settings, pane, false);
        settings.setVisibility(View.GONE);
        pane.addView(settings);
        wireSettings();
    }

    /** The app was already open and got a new link or file: import it. */
    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        handleIntent(intent);
    }

    /**
     * Back in front: listen to state changes again and refresh everything, since profiles or app
     * routing may have changed in another screen.
     */
    @Override
    protected void onResume() {
        super.onResume();
        Bus.watch(this);
        if (adapter != null) {
            adapter.reload();
        }
        onLoft();
        paintAppsRow();
    }

    /** Stops listening while the screen is hidden. */
    @Override
    protected void onPause() {
        Bus.drop(this);
        // A queued public-IP retry would keep this screen alive and use the
        // network while nobody looks; drop it, and let the next Home redraw
        // start a fresh lookup.
        if (extQueued) {
            extQueued = false;
            Ui.MAIN.removeCallbacks(extRetry);
            Live.extIP = "";
        }
        super.onPause();
    }

    /**
     * Bus callback: shared state changed, redraw Home and the connection list.
     * Runs on the main thread, at most 10 times a second (Bus merges posts).
     * The connection list shows only the session state and which profile is
     * connected, so it is rebound only when one of those changed and the
     * Connections tab is on screen; speed counters change every second but do
     * not appear there.
     */
    @Override
    public void onLoft() {
        paintHome();
        if (adapter == null || conns == null || conns.getVisibility() != View.VISIBLE) {
            return;
        }
        String sig = Live.state + "|" + Live.holding + "|" + Live.name + "|"
                + LoftVpnService.running + "|" + LoftApp.of(this).profiles().activeId();
        if (!sig.equals(listSig)) {
            listSig = sig;
            adapter.notifyDataSetChanged();
        }
    }

    /** Makes one tab view visible and hides the others, refreshing the one being shown. */
    private void show(View v) {
        home.setVisibility(v == home ? View.VISIBLE : View.GONE);
        if (conns != null) {
            conns.setVisibility(v == conns ? View.VISIBLE : View.GONE);
        }
        if (settings != null) {
            settings.setVisibility(v == settings ? View.VISIBLE : View.GONE);
        }
        if (v == conns && adapter != null) {
            adapter.reload();
            listSig = null;
        }
        if (v == home) {
            paintHome();
        }
    }

    /**
     * The views on Home, looked up once. findViewById walks the view tree;
     * paintHome runs up to ten times a second, so it uses these fields.
     */
    private static final class HomeViews {
        final ImageView lamp;
        final TextView pillT;
        final View pillDot;
        final Button go;
        final View cards;
        final View sparkRow;
        final View iprows;
        final SparkView spark;
        final TextView rateDn;
        final TextView rateUp;
        final View sparkR;
        final TextView ext;
        final TextView overlay;
        final TextView route;
        final TextView k1;
        final TextView k2;
        final TextView k3;
        final TextView v1;
        final TextView v2;
        final TextView v3;

        /** Looks every view up in the inflated Home pane. */
        HomeViews(View h) {
            lamp = h.findViewById(R.id.lamp);
            pillT = h.findViewById(R.id.pill_t);
            pillDot = h.findViewById(R.id.pill_dot);
            go = h.findViewById(R.id.go);
            cards = h.findViewById(R.id.cards);
            sparkRow = h.findViewById(R.id.spark_row);
            iprows = h.findViewById(R.id.iprows);
            spark = h.findViewById(R.id.spark);
            rateDn = h.findViewById(R.id.rate_dn);
            rateUp = h.findViewById(R.id.rate_up);
            sparkR = h.findViewById(R.id.spark_r);
            ext = h.findViewById(R.id.row_ext);
            overlay = h.findViewById(R.id.row_overlay);
            route = h.findViewById(R.id.row_route);
            k1 = h.findViewById(R.id.card_k1);
            k2 = h.findViewById(R.id.card_k2);
            k3 = h.findViewById(R.id.card_k3);
            v1 = h.findViewById(R.id.card_v1);
            v2 = h.findViewById(R.id.card_v2);
            v3 = h.findViewById(R.id.card_v3);
        }
    }

    /**
     * Redraws Home from Live: button and status pill for connecting / on /
     * error / off, the info cards, rates and chart, the overlay address
     * (this device's address inside the loft network), the gateway and the
     * public IP seen through the tunnel. "Simple" mode hides the details.
     *
     * <p>Every view is touched only when its content changes (Ui.text,
     * Ui.shown, Ui.bgRes, and the button state below): while connected only
     * the two speed labels and the chart change each second, so a redraw
     * costs almost nothing instead of a layout pass of the whole screen.
     */
    private void paintHome() {
        HomeViews h = hv;
        Profile p = LoftApp.of(this).profiles().active();
        String name = p == null ? "Home" : (p.name.isEmpty() ? p.host() : p.name);
        boolean on = Live.on();
        boolean conn = Live.connecting();
        boolean simple = Prefs.simple(this);
        // 1. Lamp, status pill and big button for the four states: 0 connecting,
        //    1 on, 2 error, 3 off. The lamp and the button look (text, tint,
        //    text colour) depend only on the state, so they are set when it
        //    changes and left alone otherwise.
        int state = conn ? 0 : on ? 1 : !Live.err.isEmpty() ? 2 : 3;
        if (state != goState) {
            goState = state;
            // A new session gets a fresh set of public-IP retries.
            if (state == 1) {
                extTries = 0;
            }
            h.lamp.setImageResource(on ? R.drawable.ic_lamp_on : R.drawable.ic_lamp_off);
            int text = state == 0 ? R.string.cancel : state == 1 ? R.string.disconnect : R.string.connect;
            int tint = state == 0 ? R.color.slate : state == 1 ? R.color.danger_bg : R.color.lamp;
            int fg = state == 0 ? R.color.snow : state == 1 ? R.color.danger_fg : R.color.ink;
            h.go.setText(text);
            h.go.setBackgroundTintList(android.content.res.ColorStateList.valueOf(getColor(tint)));
            h.go.setTextColor(getColor(fg));
        }
        if (state == 0) {
            Ui.text(h.pillT, getString(R.string.pill_busy, name));
            Ui.bgRes(h.pillDot, R.drawable.bg_dot);
        } else if (state == 1) {
            String t = getString(R.string.pill_on, name);
            if (Live.holding) {
                t = getString(R.string.hold) + " · " + t;
            }
            Ui.text(h.pillT, t);
            Ui.bgRes(h.pillDot, R.drawable.bg_dot_on);
        } else if (state == 2) {
            // Explain.display asks the system about the network; once per new error is enough.
            String err = Live.err;
            if (!err.equals(errShown)) {
                errShown = err;
                errText = Explain.display(this, err);
            }
            Ui.text(h.pillT, errText);
            Ui.bgRes(h.pillDot, R.drawable.bg_dot_err);
        } else {
            Ui.text(h.pillT, getString(R.string.pill_off_home, name));
            Ui.bgRes(h.pillDot, R.drawable.bg_dot);
        }
        // 2. Which detail blocks are visible, then the info cards.
        Ui.shown(h.cards, !simple);
        Ui.shown(h.sparkRow, !simple && (on || conn));
        Ui.shown(h.iprows, !simple && on);
        paintCards(p, on);
        // 3. Current speed and the traffic chart (a handshake animation while connecting).
        boolean bits = Prefs.rateBits(this);
        if (on && !conn) {
            Ui.text(h.rateDn, "↓ " + Cidr.fmt(Live.rateDown, bits));
            Ui.text(h.rateUp, "↑ " + Cidr.fmt(Live.rateUp, bits));
        }
        Ui.shown(h.sparkR, on && !conn);
        h.spark.setBars(Prefs.sparkBars(this));
        if (conn) {
            h.spark.setHandshake(true, getString(R.string.handshake));
        } else if (on) {
            h.spark.setHandshake(false, "");
            h.spark.setSamples(Live.sparkDn, Live.sparkUp, Live.sparkCount);
        } else {
            h.spark.clear();
        }
        // 4. Public IP (fetched on demand), overlay address(es) and gateway.
        boolean showExt = Prefs.showExtIP(this);
        Ui.shown((View) h.ext.getParent(), showExt);
        if (on && showExt && Live.extIP.isEmpty()) {
            fetchExt();
        }
        String ext = Live.extIP;
        Ui.text(h.ext, ext.isEmpty() ? "—" : ext);
        String ov = Live.ip;
        if (!Live.ip6.isEmpty()) {
            ov = ov.isEmpty() ? Live.ip6 : ov + "  " + Live.ip6;
        }
        Ui.text(h.overlay, ov.isEmpty() ? "—" : ov);
        Ui.text(h.route, Live.gateway.isEmpty() ? "—" : Live.gateway);
    }

    /**
     * Fills the three info cards for the protocol family. For loft they show the
     * front in use and the options: mux (many streams in one connection) or IP
     * for L3, "mask" when the node's mask key is known, and "hold" while the
     * session waits on a network switch. Change-only updates, like paintHome.
     */
    private void paintCards(Profile p, boolean on) {
        HomeViews h = hv;
        String fam = p == null ? "loft" : p.family();
        Ui.text(h.k1, getString(R.string.proto_k));
        if ("sstp".equals(fam)) {
            Ui.text(h.k2, getString(R.string.trans_k));
            Ui.text(h.k3, getString(R.string.auth_k));
            Ui.text(h.v1, "SSTP");
            Ui.text(h.v2, "PPP");
            Ui.text(h.v3, "CHAP");
        } else if ("vless".equals(fam) || "trojan".equals(fam)) {
            Ui.text(h.k2, getString(R.string.trans_k));
            Ui.text(h.k3, getString(R.string.sec_k));
            Ui.text(h.v1, "vless".equals(fam) ? "VLESS" : "Trojan");
            Ui.text(h.v2, p == null ? "TCP" : p.linkTransport());
            Ui.text(h.v3, p == null ? "TLS" : p.linkSecurity());
        } else {
            Ui.text(h.k2, getString(R.string.front_k));
            Ui.text(h.k3, getString(R.string.opt_k));
            Ui.text(h.v1, "loft/1.0");
            String front = Live.kind.isEmpty() && p != null ? p.kind : Live.kind;
            if (!on && (front.isEmpty() || "get".equalsIgnoreCase(front))) {
                Ui.text(h.v2, getString(R.string.kind_auto_v));
            } else {
                Ui.text(h.v2, front.isEmpty() ? "—" : front.toUpperCase());
            }
            if (!on) {
                Ui.text(h.v3, "—");
            } else {
                String opt = "l3".equalsIgnoreCase(front) ? "IP" : getString(R.string.mux);
                if (p != null && p.maskPub != null && !p.maskPub.isEmpty()) {
                    opt = opt + " · " + getString(R.string.mask);
                }
                if (Live.holding) {
                    opt = getString(R.string.hold) + " · " + opt;
                }
                Ui.text(h.v3, opt);
            }
        }
    }

    /** Bottom sheet with session details: overlay address, front, MTU and server host. */
    private void details() {
        Profile p = LoftApp.of(this).profiles().active();
        BottomSheetDialog d = Sheets.of(this, R.layout.sheet_details);
        View v = d.findViewById(R.id.det_addr);
        if (v == null) {
            return;
        }
        String ov = Live.ip.isEmpty() ? "—" : Live.ip;
        ((TextView) d.findViewById(R.id.det_addr)).setText(getString(R.string.overlay_ip) + "  " + ov);
        String front = Live.kind.isEmpty() && p != null ? p.kind : Live.kind;
        ((TextView) d.findViewById(R.id.det_front)).setText(getString(R.string.front) + "  "
                + (front.isEmpty() ? "—" : front.toUpperCase()));
        ((TextView) d.findViewById(R.id.det_mtu)).setText(getString(R.string.mtu) + "  " + Live.mtu);
        ((TextView) d.findViewById(R.id.det_server)).setText(getString(R.string.server) + "  "
                + (p == null || p.host().isEmpty() ? "—" : p.host()));
        d.show();
    }

    /** Big-button action: stop when connected or connecting, otherwise connect. */
    private void toggle() {
        if (Live.on() || Live.connecting() || LoftVpnService.running) {
            LoftVpnService.requestStop(this);
            return;
        }
        connect();
    }

    /**
     * Starts the active profile. Asks once for the notification permission
     * (API 33+), so the VPN's ongoing notification is visible, and for VPN
     * consent when the system needs it; the service starts after consent.
     * The notification prompt does not block connecting: its answer is not
     * waited for, and refusing it does not stop the VPN.
     */
    private void connect() {
        // No profile yet: there is nothing to start, so show the Connections tab instead.
        if (LoftApp.of(this).profiles().active() == null) {
            ((BottomNavigationView) findViewById(R.id.nav)).setSelectedItemId(R.id.nav_conns);
            return;
        }
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED && !askedVpn) {
            askedVpn = true;
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, REQ_NOTE);
        }
        // prepare() returns an Intent for the system "allow VPN?" dialog when this app is not
        // yet the approved VPN app, or null when it already is and we may start right away.
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            startActivityForResult(prep, REQ_VPN);
            return;
        }
        LoftVpnService.requestStart(this);
    }

    /** Result of the VPN consent dialog: start the service if the user allowed it. A refusal does nothing. */
    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_VPN && resultCode == Activity.RESULT_OK) {
            LoftVpnService.requestStart(this);
        }
    }

    /** Long-press menu for a profile: edit, export or delete. */
    private void showConnMenu(Profile pr) {
        if (pr == null) {
            return;
        }
        new AlertDialog.Builder(this)
                .setItems(new CharSequence[]{
                        getString(R.string.edit),
                        getString(R.string.export),
                        getString(R.string.delete)
                }, (d, w) -> {
                    if (w == 0) {
                        startActivity(new Intent(this, EditActivity.class).putExtra("id", pr.id));
                    } else if (w == 1) {
                        exportSheet(pr);
                    } else {
                        confirmDelete(pr);
                    }
                })
                .show();
    }

    /** Asks before deleting a profile, then refreshes the list. */
    private void confirmDelete(Profile pr) {
        new AlertDialog.Builder(this)
                .setMessage(R.string.confirm_delete)
                .setPositiveButton(R.string.delete, (a, b) -> {
                    LoftApp.of(this).profiles().delete(pr.id);
                    if (adapter != null) {
                        adapter.reload();
                    }
                })
                .setNegativeButton(R.string.cancel, null)
                .show();
    }

    /**
     * Export sheet for one profile: share it as a .loft text (its session YAML), show it as a
     * QR code (ShareQrActivity), or share every profile joined into one "Family.loft" text
     * (the same bundle a TV receives when pairing).
     */
    private void exportSheet(Profile pr) {
        BottomSheetDialog d = Sheets.of(this, R.layout.sheet_export);
        d.findViewById(R.id.ex_this).setOnClickListener(v -> {
            d.dismiss();
            shareText(pr.sessionYaml(), pr.name + ".loft");
        });
        d.findViewById(R.id.ex_qr).setOnClickListener(v -> {
            d.dismiss();
            startActivity(new Intent(this, ShareQrActivity.class).putExtra("id", pr.id));
        });
        d.findViewById(R.id.ex_all).setOnClickListener(v -> {
            d.dismiss();
            shareText(LoftApp.of(this).profiles().shareAll(), "Family.loft");
        });
        d.show();
    }

    /** Opens the system share sheet with text and a file-like subject name. */
    private void shareText(String body, String name) {
        Intent i = new Intent(Intent.ACTION_SEND);
        i.setType("text/plain");
        i.putExtra(Intent.EXTRA_TEXT, body);
        i.putExtra(Intent.EXTRA_SUBJECT, name);
        startActivity(Intent.createChooser(i, getString(R.string.export)));
    }

    /** Add-connection sheet: scan, paste into the scanner field, file, or the editor. */
    private void addSheet() {
        BottomSheetDialog d = Sheets.cards(this, R.layout.sheet_add);
        d.findViewById(R.id.add_scan).setOnClickListener(v -> {
            d.dismiss();
            startActivity(new Intent(this, ScanActivity.class));
        });
        d.findViewById(R.id.add_paste).setOnClickListener(v -> {
            d.dismiss();
            startActivity(new Intent(this, ScanActivity.class).putExtra("paste", true));
        });
        d.findViewById(R.id.add_file).setOnClickListener(v -> {
            d.dismiss();
            startActivity(new Intent(this, ScanActivity.class).putExtra("file", true));
        });
        d.findViewById(R.id.add_manual).setOnClickListener(v -> {
            d.dismiss();
            startActivity(new Intent(this, EditActivity.class));
        });
        d.findViewById(R.id.add_cancel).setOnClickListener(v -> d.dismiss());
        d.show();
    }

    /**
     * Imports from the clipboard; with an empty clipboard opens the scanner instead.
     * Not called from this screen at the moment: the add sheet's "paste" item opens
     * ScanActivity with the "paste" extra instead, so the user sees the text first.
     */
    private void paste() {
        ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
        CharSequence t = null;
        if (cm != null && cm.getPrimaryClip() != null && cm.getPrimaryClip().getItemCount() > 0) {
            t = cm.getPrimaryClip().getItemAt(0).coerceToText(this);
        }
        if (t == null || t.toString().trim().isEmpty()) {
            startActivity(new Intent(this, ScanActivity.class));
            return;
        }
        importRaw(t.toString());
    }

    /**
     * Imports from a loft:// link, text shared to the app, or an opened file. These are the
     * three intent filters declared for MainActivity in the phone manifest. A plain launcher
     * start carries none of them and is ignored. Returns true when an import was started
     * (it finishes in the background, see importRaw).
     */
    private boolean handleIntent(Intent intent) {
        if (intent == null) {
            return false;
        }
        Uri u = intent.getData();
        // 1. loft://... link (tapped in a browser or chat). The data is cleared afterwards so
        // that the same link is not imported again when the activity is recreated.
        if (u != null && "loft".equalsIgnoreCase(u.getScheme())) {
            importRaw(u.toString());
            intent.setData(null);
            return true;
        }
        // 2. Text shared to the app from another app's "Share" menu. The text is removed
        // from the intent once taken, like the link above.
        if (Intent.ACTION_SEND.equals(intent.getAction())) {
            String t = intent.getStringExtra(Intent.EXTRA_TEXT);
            intent.removeExtra(Intent.EXTRA_TEXT);
            if (t != null) {
                importRaw(t);
                return true;
            }
            return false;
        }
        // 3. A .loft file opened from a file manager or downloads; read through the content
        // resolver because the app only gets a content:// URI, not a file path. The read
        // runs in the background: the provider may be slow (a cloud drive, a download still
        // being written). Files over 1 MB are not profiles and are refused. Read errors are
        // ignored: nothing is imported.
        if (u != null && Intent.ACTION_VIEW.equals(intent.getAction())) {
            intent.setData(null);
            Ui.bg(this, () -> {
                try (InputStream in = getContentResolver().openInputStream(u)) {
                    return in == null ? null : readAll(in, 1024 * 1024);
                }
            }, (text, err) -> {
                if (text != null) {
                    importRaw(text);
                }
            });
            return true;
        }
        return false;
    }

    /**
     * Adds every profile found in the text (links or YAML, parsed by Importer) and switches to
     * the Connections tab. Importing anything also marks onboarding as done, so the first-run
     * wizard does not open on top. Text with nothing usable shows a hint toast.
     */
    void importRaw(String raw) {
        // Importer.parse calls into the Go engine and may parse a large YAML,
        // so it runs in the background; the result is applied on the main thread.
        Ui.bg(this, () -> Importer.parse(raw), (got, err) -> {
            if (got == null || got.isEmpty()) {
                Toast.makeText(this, R.string.paste_hint, Toast.LENGTH_SHORT).show();
                return;
            }
            addImported(got);
        });
    }

    /** Saves parsed profiles, shows them on the Connections tab and marks onboarding done. */
    private void addImported(List<Profile> got) {
        LoftApp.of(this).profiles().addAll(got);
        ensureConns();
        adapter.reload();
        Prefs.p(this).edit().putBoolean("onboard_done", true).apply();
        Toast.makeText(this, R.string.imported, Toast.LENGTH_SHORT).show();
        ((BottomNavigationView) findViewById(R.id.nav)).setSelectedItemId(R.id.nav_conns);
    }

    /**
     * Binds the Settings tab to Prefs. Each control writes its Prefs key as soon as it
     * changes. Routing settings (default route, split tunneling, local DNS) are not pushed
     * to the service from here: LoftVpnService listens for changes to those keys and
     * rebuilds the live TUN (the virtual network interface of the VPN) itself.
     * "Split tunneling" means only some traffic goes through the VPN, chosen by the
     * route lists. A language change recreates the screen so every string is reloaded.
     */
    private void wireSettings() {
        // 1. On/off switches, each saved to Prefs the moment it changes.
        CompoundButton simple = settings.findViewById(R.id.sw_simple);
        CompoundButton def = settings.findViewById(R.id.sw_default);
        CompoundButton split = settings.findViewById(R.id.sw_split);
        CompoundButton auto = settings.findViewById(R.id.sw_auto);
        CompoundButton re = settings.findViewById(R.id.sw_re);
        CompoundButton ext = settings.findViewById(R.id.sw_ext);
        CompoundButton log = settings.findViewById(R.id.sw_log);
        CompoundButton crash = settings.findViewById(R.id.sw_crash);
        simple.setChecked(Prefs.simple(this));
        def.setChecked(Prefs.defaultRoute(this));
        split.setChecked(Prefs.split(this));
        auto.setChecked(Prefs.autoConnect(this));
        re.setChecked(Prefs.reconnect(this));
        ext.setChecked(Prefs.showExtIP(this));
        log.setChecked(Prefs.verbose(this));
        // Crash reports are sent as GitHub issues, so the switch exists only in the GitHub
        // build; the store build hides its row.
        if (!BuildConfig.GITHUB) {
            View row = settings.findViewById(R.id.row_crash);
            if (row != null) {
                row.setVisibility(View.GONE);
            }
        } else if (crash != null) {
            crash.setChecked(Prefs.crashReports(this));
        }
        simple.setOnCheckedChangeListener((v, on) -> {
            Prefs.p(this).edit().putBoolean("simple", on).apply();
            paintHome();
        });
        def.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("default_route", on).apply());
        split.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("split", on).apply());
        auto.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("auto", on).apply());
        re.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("reconnect", on).apply());
        // Public IP switch: forget the old value and look it up again at once if connected.
        ext.setOnCheckedChangeListener((v, on) -> {
            Prefs.p(this).edit().putBoolean("ext_ip", on).apply();
            Live.extIP = "";
            extTries = 0;
            if (on && Live.on()) {
                fetchExt();
            }
            paintHome();
        });
        log.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("verbose", on).apply());
        if (BuildConfig.GITHUB && crash != null) {
            crash.setOnCheckedChangeListener((v, on) ->
                    Prefs.p(this).edit().putBoolean("crash", on).apply());
        }
        // 2. Display choices: speed units, chart style and language.
        Spinner rate = settings.findViewById(R.id.sp_rate);
        rate.setAdapter(spin(getString(R.string.rate_kbit), getString(R.string.rate_kb)));
        rate.setSelection(Prefs.rateBits(this) ? 0 : 1);
        rate.setOnItemSelectedListener(new SimplePick(i -> {
            Prefs.p(this).edit().putBoolean("rate_bits", i == 0).apply();
            paintHome();
        }));
        Spinner spark = settings.findViewById(R.id.sp_spark);
        spark.setAdapter(spin(getString(R.string.spark_lines), getString(R.string.spark_bars)));
        spark.setSelection(Prefs.sparkBars(this) ? 1 : 0);
        spark.setOnItemSelectedListener(new SimplePick(i -> {
            Prefs.p(this).edit().putBoolean("spark_bars", i == 1).apply();
            paintHome();
        }));
        Spinner lang = settings.findViewById(R.id.sp_lang);
        lang.setAdapter(spin(getString(R.string.lang_en), getString(R.string.lang_ru), getString(R.string.lang_zh)));
        lang.setSelection(Lang.index(this));
        lang.setOnItemSelectedListener(new SimplePick(i -> {
            String tag = Lang.TAGS[i];
            // Same language picked again: skip, otherwise recreate() would loop for nothing.
            if (tag.equals(Prefs.lang(this))) {
                return;
            }
            Prefs.p(this).edit().putString("lang", tag).apply();
            Lang.apply(this);
            recreate();
        }));
        // 3. Buttons that open other screens (system VPN settings, log, about, lists, apps).
        settings.findViewById(R.id.btn_vpn).setOnClickListener(v ->
                startActivity(new Intent(Settings.ACTION_VPN_SETTINGS)));
        settings.findViewById(R.id.btn_log).setOnClickListener(v ->
                startActivity(new Intent(this, LogActivity.class)));
        settings.findViewById(R.id.btn_about).setOnClickListener(v ->
                startActivity(new Intent(this, AboutActivity.class)));
        // "Lists" and "Routes" both open the same screen (ListsActivity).
        View.OnClickListener openLists = v ->
                startActivity(new Intent(this, ListsActivity.class));
        settings.findViewById(R.id.btn_lists).setOnClickListener(openLists);
        settings.findViewById(R.id.btn_routes).setOnClickListener(openLists);
        settings.findViewById(R.id.row_apps).setOnClickListener(v ->
                startActivity(new Intent(this, AppsActivity.class)));
        paintAppsRow();
        // 4. Local DNS: system, a plain DNS address, or DNS-over-HTTPS (DoH) URL.
        Spinner dns = settings.findViewById(R.id.sp_dns);
        EditText dnsAddr = settings.findViewById(R.id.dns_addr);
        dns.setAdapter(spin(getString(R.string.ldns_sys),
                getString(R.string.ldns_custom), getString(R.string.ldns_doh)));
        int dnsIdx = "doh".equals(Prefs.dnsMode(this)) ? 2
                : "ip".equals(Prefs.dnsMode(this)) ? 1 : 0;
        dnsAddr.setText(Prefs.dnsAddr(this));
        paintDnsRow(dnsIdx, dnsAddr);
        dns.setSelection(dnsIdx);
        // Spinner position to Prefs value: 0 = "sys", 1 = "ip", 2 = "doh".
        dns.setOnItemSelectedListener(new SimplePick(i -> {
            String m = i == 2 ? "doh" : i == 1 ? "ip" : "sys";
            Prefs.p(this).edit().putString("local_dns", m).apply();
            paintDnsRow(i, dnsAddr);
        }));
        // The address is saved on every keystroke. Each save is also a "local_dns_addr"
        // change that the running VPN service reacts to.
        dnsAddr.addTextChangedListener(new android.text.TextWatcher() {
            /** Not needed; the interface requires it. */
            @Override
            public void beforeTextChanged(CharSequence s, int a, int b, int c) {
            }

            /** Not needed; the interface requires it. */
            @Override
            public void onTextChanged(CharSequence s, int a, int b, int c) {
            }

            /** Saves the trimmed address after every edit. */
            @Override
            public void afterTextChanged(android.text.Editable s) {
                Prefs.p(MainActivity.this).edit()
                        .putString("local_dns_addr", s.toString().trim()).apply();
            }
        });
        // 5. Reset to defaults, then the update-channel section (Channel).
        settings.findViewById(R.id.btn_defaults).setOnClickListener(v ->
                new AlertDialog.Builder(this)
                        .setMessage(R.string.defaults_q)
                        .setPositiveButton(R.string.defaults, (a, b) -> {
                            Prefs.defaults(this);
                            Lang.apply(this);
                            recreate();
                        })
                        .setNegativeButton(R.string.cancel, null)
                        .show());
        // Channel is build-specific: the GitHub build wires its self-update button here,
        // the store build has its own Channel class (updates come from the store).
        Channel.attach(this, settings);
        // Lets the footer link buttons wrap onto more lines at a large system font size.
        Fit.buttonRow(settings.findViewById(R.id.settings_links));
    }

    /**
     * Looks up the public IP through the tunnel in the background, one lookup
     * at a time. A failure is retried after 2.5, 5, 10 and 20 s (the first
     * request may run before the tunnel carries traffic), then it gives up
     * and shows "—" until the session or the setting changes. Retries are
     * cancelled when the screen goes to the background (see onPause), and the
     * lookup holds only the application context, never this screen.
     */
    private void fetchExt() {
        if (!Prefs.showExtIP(this) || !Live.on()) {
            return;
        }
        if (!extFetch.compareAndSet(false, true)) {
            return;
        }
        // "…" is shown on Home while the lookup runs.
        Live.extIP = "…";
        final android.content.Context app = getApplicationContext();
        final String url = Prefs.extIPURL(this);
        Ui.bg(() -> {
            String got = ExtIP.lookup(app, url);
            Ui.MAIN.post(() -> {
                extFetch.set(false);
                if (!got.isEmpty()) {
                    extTries = 0;
                    Live.extIP = got;
                } else {
                    Live.extIP = "—";
                    if (extTries < 4) {
                        extQueued = true;
                        Ui.MAIN.postDelayed(extRetry, 2500L << extTries);
                        extTries++;
                    }
                }
                Bus.post();
            });
        });
    }

    /** A queued public-IP retry: try again if the session is still on and no address is known. */
    private void retryExt() {
        extQueued = false;
        if (Live.on() && Prefs.showExtIP(this)
                && ("—".equals(Live.extIP) || Live.extIP.isEmpty())) {
            Live.extIP = "";
            fetchExt();
        }
    }

    /** Reads a whole stream as UTF-8 text. Also used by ScanActivity for opened files. */
    static String readAll(InputStream in) throws Exception {
        return readAll(in, Integer.MAX_VALUE);
    }

    /** Reads a whole stream as UTF-8 text, failing past {@code max} bytes. */
    static String readAll(InputStream in, int max) throws Exception {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        byte[] b = new byte[4096];
        int n;
        int total = 0;
        while ((n = in.read(b)) > 0) {
            total += n;
            if (total > max) {
                throw new java.io.IOException("file too large");
            }
            o.write(b, 0, n);
        }
        return o.toString(StandardCharsets.UTF_8.name());
    }

    /** Shows the per-app routing mode on its Settings row, with the app count for except/only. */
    private void paintAppsRow() {
        if (settings == null) {
            return;
        }
        TextView t = settings.findViewById(R.id.apps_mode);
        if (t == null) {
            return;
        }
        String mode = Prefs.appMode(this);
        int n = Prefs.appSet(this, mode).size();
        String label = "only".equals(mode) ? getString(R.string.apps_only)
                : "except".equals(mode) ? getString(R.string.apps_except)
                : getString(R.string.apps_all);
        t.setText(n > 0 && !"all".equals(mode) ? label + " · " + n : label);
    }

    /** Hides the DNS address field for system DNS and sets a matching example otherwise. */
    private static void paintDnsRow(int i, EditText addr) {
        if (i == 0) {
            addr.setVisibility(View.GONE);
            return;
        }
        addr.setVisibility(View.VISIBLE);
        addr.setHint(i == 2 ? "https://dns.example/dns-query" : "77.88.8.8");
    }

    /** Spinner adapter with the app's own item layouts. */
    private ArrayAdapter<String> spin(String... items) {
        ArrayAdapter<String> a = new ArrayAdapter<>(this, R.layout.spinner_item, items);
        a.setDropDownViewResource(R.layout.spinner_dropdown);
        return a;
    }

    /**
     * Spinner listener that ignores the first call: Android fires
     * onItemSelected once when the initial selection is set, which would
     * otherwise act as a user choice.
     */
    private static final class SimplePick implements android.widget.AdapterView.OnItemSelectedListener {
        /** What to do with a real user choice; gets the selected position. */
        private final Seg.Pick pick;
        /** False until the initial, system-made selection has been swallowed. */
        private boolean primed;

        /** Wraps a position callback (the same Pick interface the segmented controls use). */
        SimplePick(Seg.Pick pick) {
            this.pick = pick;
        }

        /** Ignores the first call, then forwards every selection to {@link #pick}. */
        @Override
        public void onItemSelected(android.widget.AdapterView<?> parent, View view, int position, long id) {
            if (!primed) {
                primed = true;
                return;
            }
            pick.on(position);
        }

        /** Not used: these spinners always have a selection. */
        @Override
        public void onNothingSelected(android.widget.AdapterView<?> parent) {
        }
    }

    /**
     * Connection list. The active profile is highlighted; its button stops the
     * session when up. Another profile's button makes it active, stops any
     * running session and connects. Tap edits, long-press opens a menu.
     * Swipe parks the card at 84dp over Edit / Delete, as in the mock.
     */
    final class ConnAdapter extends RecyclerView.Adapter<ConnAdapter.Hold> {
        /** Snapshot copy of the saved profiles, in list order; refreshed by reload(). */
        private List<Profile> data = new ArrayList<>();
        /**
         * Id of the profile whose card is swiped open, or null. Kept by id, not by row
         * view, because RecyclerView reuses row views for other profiles while scrolling.
         */
        private String openId;
        /** Horizontal offset (px) of that open card, so it is restored after a redraw. */
        private float openTx;

        /**
         * Reloads profiles from storage, closes any swiped card and toggles the empty-list hint.
         */
        void reload() {
            data = LoftApp.of(MainActivity.this).profiles().all();
            openId = null;
            openTx = 0;
            notifyDataSetChanged();
            boolean empty = data.isEmpty();
            conns.findViewById(R.id.list).setVisibility(empty ? View.GONE : View.VISIBLE);
            conns.findViewById(R.id.empty).setVisibility(empty ? View.VISIBLE : View.GONE);
        }

        /** Profile at a position, or null when the position is out of range. */
        Profile getItem(int i) {
            return i >= 0 && i < data.size() ? data.get(i) : null;
        }

        /**
         * Snaps every swiped card except {@code keep} back to closed, so only one is open at a time.
         */
        void closeOthers(Hold keep) {
            RecyclerView rv = conns.findViewById(R.id.list);
            for (int i = 0; i < getItemCount(); i++) {
                RecyclerView.ViewHolder vh = rv.findViewHolderForAdapterPosition(i);
                if (vh instanceof Hold && vh != keep) {
                    ((Hold) vh).swipe.settleNow(0);
                }
            }
        }

        /** Inflates one connection card. */
        @NonNull
        @Override
        public Hold onCreateViewHolder(@NonNull ViewGroup parent, int viewType) {
            View v = getLayoutInflater().inflate(R.layout.row_conn, parent, false);
            return new Hold(v);
        }

        /**
         * Fills a card for one profile and wires its connect button, tap, swipe actions and
         * long-press. The open swipe position is remembered by profile id so it survives redraws.
         */
        @Override
        public void onBindViewHolder(@NonNull Hold h, int i) {
            // Texts and the "active" highlight. thisUp means this card's profile is the one
            // the VPN is running (or starting), so its button reads Disconnect.
            Profile p = data.get(i);
            boolean active = p.id.equals(LoftApp.of(MainActivity.this).profiles().activeId());
            h.title.setText(p.name.isEmpty() ? p.host() : p.name);
            h.sub.setText(p.host());
            h.kind.setText(p.side());
            h.live.setVisibility(active ? View.VISIBLE : View.GONE);
            h.card.setBackgroundResource(active ? R.drawable.bg_card_live : R.drawable.bg_card);
            boolean thisUp = active && (Live.on() || Live.connecting());
            h.mini.setText(thisUp ? R.string.disconnect : R.string.connect);
            h.mini.setBackgroundTintList(android.content.res.ColorStateList.valueOf(
                    getColor(thisUp ? R.color.lamp : R.color.slate)));
            h.mini.setTextColor(getColor(thisUp ? R.color.ink : R.color.snow));
            // Restore this card's swipe state and remember where the user leaves it.
            h.swipe.settleNow(p.id.equals(openId) ? openTx : 0);
            h.swipe.setListener(new RevealFrame.Listener() {
                /** The user started swiping this card: close any other open card. */
                @Override
                public void onRevealStart(RevealFrame row) {
                    closeOthers(h);
                }

                /**
                 * The card stopped moving at offset {@code tx}. Remember it as the open card,
                 * or forget it when it snapped back (offset under 1 px). The position is read
                 * at this moment because the holder may have been rebound since onBind.
                 */
                @Override
                public void onSettled(RevealFrame row, float tx) {
                    int pos = h.getBindingAdapterPosition();
                    Profile cur = getItem(pos);
                    if (cur == null) {
                        return;
                    }
                    if (Math.abs(tx) < 1f) {
                        if (cur.id.equals(openId)) {
                            openId = null;
                            openTx = 0;
                        }
                    } else {
                        openId = cur.id;
                        openTx = tx;
                    }
                }
            });
            // Card button: make this profile active, then stop it or (re)connect with it.
            // When another profile is running it is stopped first, then connect() starts
            // the newly active one.
            h.mini.setOnClickListener(x -> {
                LoftApp.of(MainActivity.this).profiles().setActive(p.id);
                if (thisUp) {
                    LoftVpnService.requestStop(MainActivity.this);
                } else {
                    if (LoftVpnService.running) {
                        LoftVpnService.requestStop(MainActivity.this);
                    }
                    connect();
                }
            });
            // Tap closes an open swipe first; otherwise opens the editor.
            // Edit and Delete are the buttons revealed under the swiped card.
            h.body.setOnClickListener(x -> {
                if (Math.abs(h.swipe.translation()) > 4) {
                    h.swipe.close();
                    openId = null;
                    openTx = 0;
                    return;
                }
                startActivity(new Intent(MainActivity.this, EditActivity.class).putExtra("id", p.id));
            });
            h.actEdit.setOnClickListener(x -> {
                h.swipe.settleNow(0);
                openId = null;
                openTx = 0;
                startActivity(new Intent(MainActivity.this, EditActivity.class).putExtra("id", p.id));
            });
            h.actDel.setOnClickListener(x -> {
                h.swipe.close();
                openId = null;
                openTx = 0;
                confirmDelete(p);
            });
            h.itemView.setOnLongClickListener(x -> {
                showConnMenu(p);
                return true;
            });
        }

        /** Number of profiles in the list. */
        @Override
        public int getItemCount() {
            return data.size();
        }

        /** Holds the views of one connection card so they are looked up only once. */
        final class Hold extends RecyclerView.ViewHolder {
            /** Swipeable frame that slides the card aside to reveal Edit / Delete. */
            final RevealFrame swipe;
            /** Card background; switches to the "live" style for the active profile. */
            final View card;
            /** Edit button revealed under the swiped card. */
            final View actEdit;
            /** Delete button revealed under the swiped card. */
            final View actDel;
            /** Small Connect / Disconnect button on the card. */
            final Button mini;
            /** Tappable text area of the card (opens the editor). */
            final View body;
            /** Profile name, or the host when the name is empty. */
            final TextView title;
            /** Server host line under the title. */
            final TextView sub;
            /** "Active" badge, visible only on the active profile. */
            final TextView live;
            /** Protocol label from Profile.side(), e.g. "loft · get" or "vless". */
            final TextView kind;

            /** Looks up every child view of a freshly inflated row_conn card. */
            Hold(View v) {
                super(v);
                swipe = v.findViewById(R.id.swipe);
                card = v.findViewById(R.id.card);
                actEdit = v.findViewById(R.id.act_edit);
                actDel = v.findViewById(R.id.act_del);
                mini = v.findViewById(R.id.mini);
                body = v.findViewById(R.id.body);
                title = v.findViewById(R.id.title);
                sub = v.findViewById(R.id.sub);
                live = v.findViewById(R.id.live);
                kind = v.findViewById(R.id.kind);
            }
        }
    }
}
