/*
 * Android client, TV build: the whole Android TV interface in one activity
 * (Home, Connections and Settings panes behind a left rail, driven by a D-pad
 * remote). It plays the role MainActivity plays on the phone and uses the same
 * LoftVpnService, Live and Bus. Imports come from a phone through TvPair, a
 * USB or Downloads file, or a typed loft:// link.
 */
package app.loft.android;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.Typeface;
import android.net.Uri;
import android.net.VpnService;
import android.os.Bundle;
import android.provider.Settings;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.ImageButton;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.Switch;
import android.widget.TextView;
import android.widget.Toast;



import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.List;

/**
 * 10-foot Leanback shell. Same LoftVpnService as the phone.
 * Import: QR on this TV, USB/Downloads, or type loft://.
 *
 * <p>"10-foot" means a UI read from the sofa and driven by a D-pad remote:
 * big type, focusable tiles, a left rail with Home, Connections and
 * Settings. Panes are views swapped in one activity, like the phone's
 * MainActivity, and redraw from Live on every Bus event.
 */
public final class TvActivity extends LoftActivity implements Bus.Listener {
    /** Request codes for the system VPN consent dialog and the file picker. */
    private static final int REQ_VPN = 1;
    private static final int REQ_FILE = 2;

    /** Container that holds the three panes; only one is visible at a time. */
    private ViewGroup pane;
    /** The panes. Connections and Settings are inflated lazily (null until first use). */
    private View home;
    private View conns;
    private View settings;
    /**
     * The full-screen panel (pairing QR, share, paste, edit) currently shown
     * over the active pane, or null. Back closes it before leaving the app.
     */
    private View overlay;
    /**
     * Id of the connection card that last had D-pad focus: the target of the
     * edit / share / delete buttons. "" means none yet; paintConns() then
     * picks the active profile.
     */
    private String selected = "";
    /** Running pairing listener while the pairing QR is shown, else null. */
    private TvPair pair;
    /** True while a public-IP lookup is in flight, so only one runs at a time. */
    private final java.util.concurrent.atomic.AtomicBoolean extFetch = new java.util.concurrent.atomic.AtomicBoolean();
    /**
     * Pauses between public-IP retries after a failed lookup: 2.5 s, 5 s,
     * 10 s, then no more until the connection state changes. The old code
     * retried every 2.5 s forever, which on a tunnel without internet meant
     * a network request (with an 8 s timeout) running non-stop.
     */
    private static final long[] EXT_RETRY_MS = {2500, 5000, 10000};
    /** Failed public-IP lookups since the last state change; indexes EXT_RETRY_MS. */
    private int extFails;
    /** True while a retry is waiting in the main-thread queue (see extRetry). */
    private boolean extRetryQueued;
    /**
     * Delayed retry of the public-IP lookup. Kept as a field so onPause and
     * onDestroy can take it back out of the queue: a hidden or closed screen
     * must not keep making network requests.
     */
    private final Runnable extRetry = () -> {
        extRetryQueued = false;
        if (Live.on() && Prefs.showExtIP(this) && "—".equals(Live.extIP)) {
            Live.extIP = "";
            fetchExt();
        }
    };
    /** Live.state seen by the last paintHome(); a change restarts the public-IP retries. */
    private String lastState = "";

    /*
     * Home pane views, looked up once in onCreate. findViewById walks the
     * view tree, and paintHome() runs on every Bus event (up to 10 times a
     * second), so the lookups are cached instead of repeated.
     */
    private ImageView hLamp;
    private TextView hStatus;
    private Button hGo;
    private TextView hName;
    private TextView hKind;
    private TextView hHost;
    private TextView hIp;
    private TextView hExt;
    private TextView hNet;
    private TextView hRate;
    private View hSide;
    /** Image resource now in hLamp, so it is only set when it changes (0 = none yet). */
    private int lampRes;
    /**
     * Last Live.err turned into display text, and that text. Explain.display()
     * asks the system about the current networks (ConnectivityManager), which
     * is too slow to repeat on every redraw, so it runs only when the error
     * itself changes.
     */
    private String errRaw;
    private String errText = "";
    /**
     * What the connection cards were last built from (see connsSig()). The
     * cards are rebuilt only when this changes; "" forces a rebuild.
     */
    private String connsSig = "";

    /**
     * Builds the rail and Home pane, opens Settings instead when asked by the
     * "pane" extra (used after a language change), imports a loft:// link the
     * app was opened with, and auto-connects when that setting is on. The
     * other panes are built right after the first frame.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        // The TV theme must be set before super.onCreate() inflates anything.
        setTheme(R.style.Theme_Loft_Tv);
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_tv);
        pane = findViewById(R.id.pane);
        home = getLayoutInflater().inflate(R.layout.pane_tv_home, pane, false);
        pane.addView(home);
        hLamp = home.findViewById(R.id.lamp);
        hStatus = home.findViewById(R.id.status);
        hGo = home.findViewById(R.id.go);
        hName = home.findViewById(R.id.side_name);
        hKind = home.findViewById(R.id.side_kind);
        hHost = home.findViewById(R.id.side_host);
        hIp = home.findViewById(R.id.side_ip);
        hExt = home.findViewById(R.id.side_ext);
        hNet = home.findViewById(R.id.side_net);
        hRate = home.findViewById(R.id.side_rate);
        hSide = home.findViewById(R.id.side);

        findViewById(R.id.rail_home).setOnClickListener(v -> show(home));
        findViewById(R.id.rail_conns).setOnClickListener(v -> {
            ensureConns();
            show(conns);
        });
        findViewById(R.id.rail_settings).setOnClickListener(v -> {
            ensureSettings();
            show(settings);
        });

        home.findViewById(R.id.go).setOnClickListener(v -> toggle());
        if ("settings".equals(getIntent().getStringExtra("pane"))) {
            ensureSettings();
            show(settings);
        } else {
            show(home);
        }
        handleIntent(getIntent());
        if (Prefs.autoConnect(this) && LoftApp.of(this).profiles().active() != null
                && !LoftVpnService.running && !Live.on()) {
            connect();
        }
        // Inflate the other two panes after the first frame is drawn, so the
        // Home screen appears quickly and switching panes later is instant.
        pane.post(() -> {
            ensureConns();
            ensureSettings();
        });
    }

    /** Inflates the Connections pane and wires its edit / share / delete actions, once. */
    private void ensureConns() {
        if (conns != null) {
            return;
        }
        conns = getLayoutInflater().inflate(R.layout.pane_tv_conns, pane, false);
        conns.setVisibility(View.GONE);
        pane.addView(conns);
        conns.findViewById(R.id.act_edit).setOnClickListener(v -> editSelected());
        conns.findViewById(R.id.act_share).setOnClickListener(v -> shareSelected());
        conns.findViewById(R.id.act_del).setOnClickListener(v -> deleteSelected());
        paintConns();
    }

    /** Inflates the Settings pane and wires its controls, once. */
    private void ensureSettings() {
        if (settings != null) {
            return;
        }
        settings = getLayoutInflater().inflate(R.layout.pane_tv_settings, pane, false);
        settings.setVisibility(View.GONE);
        pane.addView(settings);
        wireSettings();
    }

    /** The activity is reused: handle a new "pane" extra or loft:// link. */
    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        if ("settings".equals(intent.getStringExtra("pane"))) {
            ensureSettings();
            show(settings);
        }
        handleIntent(intent);
    }

    /** Starts listening to Bus (state change events) and redraws. */
    @Override
    protected void onResume() {
        super.onResume();
        visible = true;
        Bus.watch(this);
        // A public-IP retry was cancelled by onPause: clear the "—" so
        // paintHome() below starts the lookup again right away.
        if (extRetryPaused && "—".equals(Live.extIP)) {
            Live.extIP = "";
        }
        extRetryPaused = false;
        paintHome();
        paintConns();
    }

    /** Set when a public-IP retry was cancelled or skipped while hidden, so onResume can resume it. */
    private boolean extRetryPaused;
    /** True between onResume and onPause. */
    private boolean visible;

    /**
     * Stops listening to Bus while the screen is hidden, and cancels a waiting
     * public-IP retry: nobody sees the result, so no network request is made.
     */
    @Override
    protected void onPause() {
        Bus.drop(this);
        visible = false;
        if (extRetryQueued) {
            Ui.MAIN.removeCallbacks(extRetry);
            extRetryQueued = false;
            extRetryPaused = true;
        }
        super.onPause();
    }

    /**
     * Makes sure the pairing listener and a waiting public-IP retry do not
     * outlive the screen (the retry would otherwise keep this activity alive).
     */
    @Override
    protected void onDestroy() {
        stopPair();
        Ui.MAIN.removeCallbacks(extRetry);
        extRetryQueued = false;
        super.onDestroy();
    }

    /**
     * Bus callback: connection state or stats in Live changed, so redraw.
     * While connected this fires at least once a second (the VPN service's
     * stats tick) plus once per new journal line. Both paint methods touch
     * only what really changed, so a tick usually costs almost nothing.
     */
    @Override
    public void onLoft() {
        paintHome();
        paintConns();
    }

    /** Back closes an open overlay first; otherwise it leaves as usual. */
    @Override
    public void onBackPressed() {
        if (overlay != null) {
            hideOverlay();
            return;
        }
        super.onBackPressed();
    }

    /** The remote's Menu key on the Connections pane opens the add dialog. */
    @Override
    public boolean onKeyDown(int keyCode, KeyEvent event) {
        if (keyCode == KeyEvent.KEYCODE_MENU && conns != null && conns.getVisibility() == View.VISIBLE) {
            addSheet();
            return true;
        }
        return super.onKeyDown(keyCode, event);
    }

    /**
     * Switches the visible pane (Home, Connections, Settings), highlights its
     * rail icon and moves D-pad focus to a sensible control on that pane.
     */
    private void show(View v) {
        hideOverlay();
        home.setVisibility(v == home ? View.VISIBLE : View.GONE);
        if (conns != null) {
            conns.setVisibility(v == conns ? View.VISIBLE : View.GONE);
        }
        if (settings != null) {
            settings.setVisibility(v == settings ? View.VISIBLE : View.GONE);
        }
        ImageButton rh = findViewById(R.id.rail_home);
        ImageButton rc = findViewById(R.id.rail_conns);
        ImageButton rs = findViewById(R.id.rail_settings);
        tintRail(v == home ? rh : v == conns ? rc : rs);
        // Give the new pane a focused control, otherwise the D-pad would have
        // nothing to move from.
        if (v == conns) {
            paintConns();
            focusConns();
        }
        if (v == home) {
            paintHome();
            home.findViewById(R.id.go).requestFocus();
        }
        if (v == settings) {
            int i = Lang.index(this);
            int[] ids = {R.id.lang_en, R.id.lang_ru, R.id.lang_zh};
            settings.findViewById(ids[i]).requestFocus();
        }
    }

    /** Colors the active rail icon amber ("lamp") and the others grey ("mute"). */
    private void tintRail(ImageButton on) {
        int lamp = 0xFFE8B84A;
        int mute = 0xFF8A7E72;
        ImageButton[] all = {
                findViewById(R.id.rail_home),
                findViewById(R.id.rail_conns),
                findViewById(R.id.rail_settings)
        };
        for (ImageButton b : all) {
            b.setColorFilter(b == on ? lamp : mute);
        }
    }

    /**
     * Redraws Home from Live: status line, connect button and the side panel
     * with overlay address (this device's address inside the loft network),
     * public IP, underlying network and rates. With no profiles the button is
     * replaced by three add tiles. Runs on every Bus event, so it uses the
     * cached views and Ui.text / Ui.shown: a value that did not change does
     * not touch its view (and so does not trigger a new layout pass).
     */
    private void paintHome() {
        // 1. Status line and connect button text from the connection state.
        Profile p = LoftApp.of(this).profiles().active();
        String name = p == null ? "" : p.name;
        boolean on = Live.on();
        boolean conn = Live.connecting();
        int lamp = on ? R.drawable.ic_lamp_on : R.drawable.ic_lamp_off;
        if (lamp != lampRes) {
            lampRes = lamp;
            hLamp.setImageResource(lamp);
        }
        if (conn) {
            Ui.text(hStatus, getString(R.string.connecting) + (name.isEmpty() ? "" : " " + name));
            Ui.text(hGo, getString(R.string.cancel));
        } else if (on) {
            Ui.text(hStatus, getString(R.string.connected) + (name.isEmpty() ? "" : " · " + name)
                    + uptime());
            Ui.text(hGo, getString(R.string.disconnect));
        } else if (!Live.err.isEmpty()) {
            // Explain.display() queries the system; redo it only for a new error.
            String err = Live.err;
            if (!err.equals(errRaw)) {
                errRaw = err;
                errText = Explain.display(this, err);
            }
            Ui.text(hStatus, errText);
            Ui.text(hGo, getString(R.string.connect));
        } else {
            Ui.text(hStatus, getString(R.string.pill_off) + (name.isEmpty() ? "" : " · " + name));
            Ui.text(hGo, getString(R.string.connect));
        }
        // 2. Side panel: the active profile, then live addresses and rates.
        if (p == null) {
            Ui.text(hName, getString(R.string.empty_conns));
            Ui.text(hKind, "");
            Ui.text(hHost, "");
        } else {
            Ui.text(hName, p.name.isEmpty() ? p.host() : p.name);
            Ui.text(hKind, p.side());
            Ui.text(hHost, p.host());
        }
        String ov = Live.ip.isEmpty() ? "—" : Live.ip;
        if (!Live.ip6.isEmpty()) {
            ov = Live.ip.isEmpty() ? Live.ip6 : Live.ip + "  " + Live.ip6;
        }
        Ui.text(hIp, getString(R.string.overlay_ip) + "  " + ov);
        Ui.text(hExt, getString(R.string.ext_ip) + "  " + (Live.extIP.isEmpty() ? "—" : Live.extIP));
        String net = Live.underlying.isEmpty() ? "—" : Live.underlying;
        Ui.text(hNet, net);
        boolean bits = Prefs.rateBits(this);
        Ui.text(hRate, "↓ " + Cidr.fmt(Live.rateDown, bits) + "   ↑ " + Cidr.fmt(Live.rateUp, bits));
        // 3. Live rows only while connected; fetch the public IP if missing.
        Ui.shown(hIp, on);
        boolean showExt = Prefs.showExtIP(this);
        Ui.shown(hExt, on && showExt);
        Ui.shown(hNet, on);
        Ui.shown(hRate, on);
        // A new connection state starts the public-IP retries from scratch
        // (fetchExt gives up after a few failures; see EXT_RETRY_MS). A "—"
        // left by an earlier give-up is cleared so the lookup runs again.
        String st = Live.state;
        if (!st.equals(lastState)) {
            lastState = st;
            extFails = 0;
            Ui.MAIN.removeCallbacks(extRetry);
            extRetryQueued = false;
            if (on && "—".equals(Live.extIP)) {
                Live.extIP = "";
            }
        }
        if (on && showExt && Live.extIP.isEmpty()) {
            fetchExt();
        }
        // 4. Nothing to show at all: hide the panel and offer add tiles.
        Ui.shown(hSide, !(p == null && !on));
        if (p == null && !on && !conn) {
            Ui.shown(hGo, false);
            showEmptyAdd();
        } else {
            Ui.shown(hGo, true);
            hideEmptyAdd();
        }
    }

    /** "  12m" minutes since connect, or "" when not connected. */
    private String uptime() {
        if (Live.since <= 0) {
            return "";
        }
        long s = Math.max(0, (System.currentTimeMillis() - Live.since) / 1000);
        return "  " + (s / 60) + "m";
    }

    /** Row of add tiles shown on Home when there are no profiles; built on first need. */
    private View emptyAdd;

    /** Builds the add tiles (phone QR, USB file, type a link) once, above the button. */
    private void showEmptyAdd() {
        if (emptyAdd != null) {
            Ui.shown(emptyAdd, true);
            return;
        }
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.HORIZONTAL);
        box.addView(addTile(getString(R.string.add_qr), this::showPair));
        box.addView(addTile(getString(R.string.add_usb), this::pickFile));
        box.addView(addTile(getString(R.string.add_type), this::showPaste));
        // Insert the row into the button's own column, just above the button.
        LinearLayout col = (LinearLayout) ((View) home.findViewById(R.id.go)).getParent();
        int i = col.indexOfChild(home.findViewById(R.id.go));
        col.addView(box, i);
        emptyAdd = box;
    }

    /** Hides the add tiles once a profile exists or a session is up. */
    private void hideEmptyAdd() {
        Ui.shown(emptyAdd, false);
    }

    /**
     * One large focusable tile button that runs {@code run} when pressed. The
     * tv_focus background draws the highlight ring that shows D-pad focus.
     */
    private Button addTile(String title, Runnable run) {
        Button b = new Button(this);
        b.setText(title);
        b.setAllCaps(false);
        b.setTextColor(0xFFF3EDE4);
        b.setTextSize(18);
        b.setBackgroundResource(R.drawable.tv_focus);
        b.setMinHeight(dp(88));
        b.setMinWidth(dp(200));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                0, ViewGroup.LayoutParams.WRAP_CONTENT, 1);
        lp.setMarginEnd(dp(12));
        b.setLayoutParams(lp);
        b.setOnClickListener(v -> run.run());
        return b;
    }

    /**
     * Rebuilds the row of connection cards plus a "+" card, but only when what
     * the cards show changed (see connsSig()). This runs on every Bus event,
     * which while connected is at least once a second; rebuilding every time
     * threw away and recreated all cards, re-laid out the pane and (worse)
     * moved D-pad focus back to the first card each second. Now a refresh with
     * nothing new does nothing, and never moves focus by itself: after a real
     * rebuild, focus goes back to the same profile's card only if focus was in
     * the row before. Giving the pane a first focus is show()'s job
     * (focusConns()). A hidden pane is not rebuilt; show() repaints it.
     */
    private void paintConns() {
        if (conns == null || conns.getVisibility() != View.VISIBLE) {
            return;
        }
        List<Profile> all = LoftApp.of(this).profiles().all();
        String active = LoftApp.of(this).profiles().activeId();
        if (selected.isEmpty() && !active.isEmpty()) {
            selected = active;
        }
        String sig = connsSig(all, active);
        if (sig.equals(connsSig)) {
            return;
        }
        connsSig = sig;
        LinearLayout row = conns.findViewById(R.id.row);
        // Remember whether the user was moving around the cards, so focus can
        // be put back on the same card after the rebuild.
        boolean hadFocus = row.hasFocus();
        row.removeAllViews();
        for (Profile p : all) {
            row.addView(connCard(p, p.id.equals(active), p.id.equals(selected)));
        }
        Button plus = new Button(this);
        plus.setText("+");
        plus.setTextSize(28);
        plus.setTextColor(0xFFE8B84A);
        plus.setBackgroundResource(R.drawable.tv_focus);
        plus.setMinWidth(dp(88));
        plus.setMinHeight(dp(120));
        plus.setOnClickListener(v -> addSheet());
        row.addView(plus);
        if (hadFocus) {
            focusConns();
        }
    }

    /**
     * Everything the connection cards are built from, as one string: each
     * profile's id, name, kind and host, plus the active profile (drawn in
     * amber). The cards show no live connection data, so traffic ticks do not
     * change it. Comparing this string is far cheaper than rebuilding views.
     */
    private static String connsSig(List<Profile> all, String active) {
        StringBuilder b = new StringBuilder(active).append('\n');
        for (Profile p : all) {
            b.append(p.id).append('\t').append(p.name).append('\t')
                    .append(p.side()).append('\t').append(p.host()).append('\n');
        }
        return b.toString();
    }

    /**
     * Moves D-pad focus to the selected profile's card, or the first card, or
     * the "+" card when there are no profiles. Used when the pane is opened
     * and to put focus back after a rebuild or a closed overlay.
     */
    private void focusConns() {
        if (conns == null) {
            return;
        }
        LinearLayout row = conns.findViewById(R.id.row);
        if (row.getChildCount() == 0) {
            return;
        }
        View target = row.findViewWithTag(selected.isEmpty() ? null : selected);
        if (target == null) {
            target = row.getChildAt(0);
        }
        target.requestFocus();
    }

    /**
     * One connection card: name (amber when it is the active profile), kind and
     * host. Click makes it active and goes Home; focus selects it for the
     * action buttons; long press opens the editor. {@code sel} enlarges it.
     */
    private View connCard(Profile p, boolean live, boolean sel) {
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.VERTICAL);
        c.setBackgroundResource(R.drawable.tv_focus);
        int pad = dp(16);
        c.setPadding(pad, pad, pad, pad);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(dp(220), dp(120));
        lp.setMarginEnd(dp(16));
        c.setLayoutParams(lp);
        c.setFocusable(true);
        c.setClickable(true);
        // The profile id lets focusConns() find this card again.
        c.setTag(p.id);
        TextView n = tv(20, live ? 0xFFE8B84A : 0xFFF3EDE4);
        n.setText(p.name.isEmpty() ? p.host() : p.name);
        TextView k = tv(16, 0xFFB7A99A);
        k.setText(p.side());
        TextView h = tv(14, 0xFF8A7E72);
        h.setText(p.host());
        c.addView(n);
        c.addView(k);
        c.addView(h);
        if (sel) {
            c.setScaleX(1.06f);
            c.setScaleY(1.06f);
        }
        // OK on the remote: make this the active profile and go back Home.
        c.setOnClickListener(v -> {
            LoftApp.of(this).profiles().setActive(p.id);
            selected = p.id;
            show(home);
        });
        c.setOnFocusChangeListener((v, has) -> {
            if (has) {
                selected = p.id;
                // The selected card is drawn slightly larger. Cards used to be
                // rebuilt every second, which redrew that; now they are not,
                // so the size follows the focus here instead.
                ViewGroup row = (ViewGroup) v.getParent();
                for (int i = 0; row != null && i < row.getChildCount(); i++) {
                    View o = row.getChildAt(i);
                    if (o.getTag() != null) {
                        float sc = o == v ? 1.06f : 1f;
                        o.setScaleX(sc);
                        o.setScaleY(sc);
                    }
                }
            }
        });
        c.setOnLongClickListener(v -> {
            selected = p.id;
            editSelected();
            return true;
        });
        return c;
    }

    /** Plain TextView with a text size in sp and a color. */
    private TextView tv(int sp, int color) {
        TextView t = new TextView(this);
        t.setTextSize(sp);
        t.setTextColor(color);
        return t;
    }

    /** Dialog with the three ways to add a connection: phone QR, file, or typed link. */
    private void addSheet() {
        new AlertDialog.Builder(this)
                .setItems(new CharSequence[]{
                        getString(R.string.add_qr),
                        getString(R.string.add_usb),
                        getString(R.string.add_type)
                }, (d, w) -> {
                    if (w == 0) {
                        showPair();
                    } else if (w == 1) {
                        pickFile();
                    } else {
                        showPaste();
                    }
                })
                .show();
    }

    /**
     * Pairing with a phone. Typing a long link with a remote is painful, so
     * the TV starts a small LAN web listener (TvPair) and shows its URL as a
     * QR. The phone app scans it and POSTs all its profiles; they are imported
     * here. The listener stops when the overlay closes.
     */
    private void showPair() {
        stopPair();
        pair = new TvPair();
        if (!pair.start(this::importRaw)) {
            Toast.makeText(this, R.string.pair_no_lan, Toast.LENGTH_SHORT).show();
            return;
        }
        // Overlay: QR with the pairing URL, the code in large monospace, a
        // hint, and the URL as text for phones without a camera.
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setBackgroundColor(0xFF1A1714);
        box.setPadding(dp(32), dp(24), dp(32), dp(24));
        box.setGravity(Gravity.CENTER_HORIZONTAL);
        ImageView qr = new ImageView(this);
        qrLater(qr, pair.url);
        qr.setAdjustViewBounds(true);
        qr.setMaxHeight(dp(280));
        TextView code = tv(34, 0xFFE8B84A);
        code.setTypeface(Typeface.MONOSPACE);
        code.setText(pair.code);
        TextView hint = tv(16, 0xFF8A7E72);
        hint.setText(R.string.pair_hint);
        TextView url = tv(14, 0xFFB7A99A);
        url.setText(pair.url);
        url.setTextIsSelectable(true);
        box.addView(qr);
        box.addView(code);
        box.addView(hint);
        box.addView(url);
        showOverlay(box);
    }

    /**
     * Shows the focused connection (or the active one) as a share link and
     * QR, with a warning that it contains the key. The link is the profile
     * written back out by YamlBuilder.
     */
    private void shareSelected() {
        Profile p = LoftApp.of(this).profiles().get(selected);
        if (p == null) {
            p = LoftApp.of(this).profiles().active();
        }
        if (p == null) {
            return;
        }
        String uri = YamlBuilder.emit(p);
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setBackgroundColor(0xFF1A1714);
        box.setPadding(dp(32), dp(24), dp(32), dp(24));
        box.setGravity(Gravity.CENTER_HORIZONTAL);
        ImageView qr = new ImageView(this);
        qrLater(qr, uri);
        qr.setAdjustViewBounds(true);
        qr.setMaxHeight(dp(280));
        TextView line = tv(14, 0xFFB7A99A);
        line.setText(uri);
        line.setTextIsSelectable(true);
        TextView warn = tv(16, 0xFFE8B84A);
        warn.setText(R.string.share_warn);
        box.addView(qr);
        box.addView(line);
        box.addView(warn);
        showOverlay(box);
    }

    /**
     * Draws the QR code for {@code text} into {@code qr} once it is ready.
     * Encoding a 512 px QR takes long enough to freeze the remote's response,
     * so it runs on the background pool (Ui.bg); the picture is set on the
     * main thread, but only if the image view is still on screen (the
     * overlay may have been closed meanwhile).
     */
    private void qrLater(ImageView qr, String text) {
        Ui.bg(this, () -> Qr.encode(text, 512), (Bitmap bmp, Exception err) -> {
            if (bmp != null && qr.isAttachedToWindow()) {
                qr.setImageBitmap(bmp);
            }
        });
    }

    /**
     * Overlay with a text field for typing or pasting a link (or a whole
     * config), and an Add button that feeds it to importRaw().
     */
    private void showPaste() {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setBackgroundColor(0xFF1A1714);
        box.setPadding(dp(32), dp(24), dp(32), dp(24));
        EditText f = new EditText(this);
        f.setHint(R.string.paste_hint);
        f.setTextColor(0xFFF3EDE4);
        f.setHintTextColor(0xFF8A7E72);
        f.setMinHeight(dp(64));
        Button add = new Button(this);
        add.setText(R.string.add);
        add.setAllCaps(false);
        add.setBackgroundResource(R.drawable.tv_focus);
        add.setTextColor(0xFFF3EDE4);
        add.setOnClickListener(v -> importRaw(f.getText().toString()));
        box.addView(f);
        box.addView(add);
        showOverlay(box);
        f.requestFocus();
    }

    /** Opens the system file picker (USB stick, Downloads); the result lands in onActivityResult. */
    private void pickFile() {
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("*/*");
        startActivityForResult(i, REQ_FILE);
    }

    /**
     * Small TV editor: name and, for loft, the front. A "front" is the
     * transport a loft profile uses to reach its server (plain HTTP GET,
     * WebSocket, HTTP/2, QUIC, HTTP/3 or L3). The "Auto" chip stores GET
     * because the config already uses "select: auto". Other protocols
     * (SSTP, VLESS, Trojan) have no front, so their kind is left alone.
     */
    private void editSelected() {
        Profile p = LoftApp.of(this).profiles().get(selected);
        if (p == null) {
            p = LoftApp.of(this).profiles().active();
        }
        if (p == null) {
            return;
        }
        // 1. Name field.
        Profile ed = p;
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setBackgroundColor(0xFF1A1714);
        box.setPadding(dp(32), dp(24), dp(32), dp(24));
        EditText name = new EditText(this);
        name.setText(ed.name);
        name.setHint(R.string.name);
        name.setTextColor(0xFFF3EDE4);
        name.setHintTextColor(0xFF8A7E72);
        box.addView(name);
        // 2. Front chips; a press only remembers the choice until Save.
        LinearLayout chips = new LinearLayout(this);
        chips.setOrientation(LinearLayout.HORIZONTAL);
        String[] ids = {"get", "get", "ws", "h2", "quic", "h3", "l3"};
        String[] titles = {
                getString(R.string.method_auto), "GET", "WS", "H2", "QUIC", "H3", "L3"
        };
        // first chip Auto still stores get (YAML select: auto).
        final String[] kind = {ed.kind};
        for (int i = 0; i < titles.length; i++) {
            Button ch = new Button(this);
            ch.setText(titles[i]);
            ch.setAllCaps(false);
            ch.setBackgroundResource(R.drawable.tv_focus);
            ch.setTextColor(0xFFF3EDE4);
            String k = ids[i];
            ch.setOnClickListener(v -> kind[0] = k);
            chips.addView(ch);
        }
        box.addView(chips);
        // 3. Save writes the profile (front only for loft) and redraws.
        Button save = new Button(this);
        save.setText(R.string.save);
        save.setAllCaps(false);
        save.setBackgroundResource(R.drawable.tv_focus);
        save.setTextColor(0xFFF3EDE4);
        save.setOnClickListener(v -> {
            ed.name = name.getText().toString().trim();
            if (ed.isLoft()) {
                ed.kind = kind[0];
            }
            LoftApp.of(this).profiles().put(ed);
            hideOverlay();
            paintConns();
            focusConns();
            paintHome();
        });
        box.addView(save);
        showOverlay(box);
        name.requestFocus();
    }

    /** Asks for confirmation, then deletes the focused connection. */
    private void deleteSelected() {
        Profile p = LoftApp.of(this).profiles().get(selected);
        if (p == null) {
            return;
        }
        new AlertDialog.Builder(this)
                .setMessage(R.string.confirm_delete)
                .setPositiveButton(R.string.delete, (a, b) -> {
                    LoftApp.of(this).profiles().delete(p.id);
                    selected = "";
                    paintConns();
                    focusConns();
                    paintHome();
                })
                .setNegativeButton(R.string.cancel, null)
                .show();
    }

    /**
     * Adds every profile found in the text (a link, a config file or the
     * phone's pairing body), closes pairing and shows Connections. Called on
     * the main thread, including from TvPair through its Handler.
     */
    private void importRaw(String raw) {
        List<Profile> list = Importer.parse(raw);
        if (list.isEmpty()) {
            Toast.makeText(this, R.string.paste_hint, Toast.LENGTH_SHORT).show();
            return;
        }
        LoftApp.of(this).profiles().addAll(list);
        stopPair();
        hideOverlay();
        Toast.makeText(this, R.string.imported, Toast.LENGTH_SHORT).show();
        show(conns);
        paintHome();
    }

    /** Shows {@code v} full-screen over the current pane, replacing any earlier overlay. */
    private void showOverlay(View v) {
        hideOverlay();
        FrameLayout pane = findViewById(R.id.pane);
        overlay = v;
        // Clickable so taps and key presses do not fall through to the pane below.
        pane.addView(v, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        v.setClickable(true);
        v.requestFocus();
    }

    /** Removes the overlay, if any, and stops pairing that it may have started. */
    private void hideOverlay() {
        if (overlay != null) {
            ((ViewGroup) overlay.getParent()).removeView(overlay);
            overlay = null;
            // The overlay held focus; give it back to the cards when closing
            // over Connections. (The old once-a-second rebuild used to do this
            // by accident; now nothing else would.)
            if (conns != null && conns.getVisibility() == View.VISIBLE) {
                focusConns();
            }
        }
        stopPair();
    }

    /** Stops the pairing listener, if one is running. */
    private void stopPair() {
        if (pair != null) {
            pair.stop();
            pair = null;
        }
    }

    /**
     * Connect button: stops a running or starting session, otherwise connects.
     * LoftVpnService.running is checked too, because the service may already
     * be up before Live has reported a state.
     */
    private void toggle() {
        if (Live.on() || Live.connecting() || LoftVpnService.running) {
            LoftVpnService.requestStop(this);
            return;
        }
        connect();
    }

    /**
     * Starts the active profile, asking for VPN consent first when the system
     * needs it. VpnService.prepare() returns an Intent only until the user has
     * accepted this app as a VPN once. With no profile, opens Connections.
     */
    private void connect() {
        if (LoftApp.of(this).profiles().active() == null) {
            ensureConns();
            show(conns);
            return;
        }
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            startActivityForResult(prep, REQ_VPN);
            return;
        }
        LoftVpnService.requestStart(this);
    }

    /** Largest picked file that is read; the rest is ignored. Config files are far smaller. */
    private static final int MAX_FILE = 256 * 1024;

    /**
     * VPN consent granted: start the session. File picked: read up to
     * 256 KiB of it and import the profiles inside. The file may live on a
     * slow USB stick or a cloud provider, so it is read on the background
     * pool (Ui.bg), not the main thread; the import runs back on the main
     * thread, and only if this screen is still open.
     */
    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_VPN && resultCode == Activity.RESULT_OK) {
            LoftVpnService.requestStart(this);
            return;
        }
        if (requestCode == REQ_FILE && resultCode == RESULT_OK && data != null && data.getData() != null) {
            Uri u = data.getData();
            ContentResolver cr = getContentResolver();
            Ui.bg(this, () -> readCapped(cr, u), (String text, Exception e) -> {
                if (e != null) {
                    Toast.makeText(this, e.getMessage(), Toast.LENGTH_SHORT).show();
                } else if (text != null && !text.isEmpty()) {
                    importRaw(text);
                }
            });
        }
    }

    /**
     * Reads the file at {@code u} as UTF-8, at most MAX_FILE bytes. A single
     * read() may return only part of a file (providers hand data over in
     * chunks), so this loops until the end of the file or the cap. Runs on a
     * background thread.
     */
    private static String readCapped(ContentResolver cr, Uri u) throws Exception {
        try (InputStream in = cr.openInputStream(u)) {
            if (in == null) {
                return "";
            }
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] buf = new byte[16 * 1024];
            int n;
            while (out.size() < MAX_FILE
                    && (n = in.read(buf, 0, Math.min(buf.length, MAX_FILE - out.size()))) >= 0) {
                out.write(buf, 0, n);
            }
            return new String(out.toByteArray(), StandardCharsets.UTF_8);
        }
    }

    /**
     * Imports a loft:// link the activity was opened with, then clears it so
     * a recreate does not import it again.
     */
    private void handleIntent(Intent in) {
        if (in == null) {
            return;
        }
        Uri u = in.getData();
        if (u != null && "loft".equals(u.getScheme())) {
            importRaw(u.toString());
            in.setData(null);
        }
    }

    /**
     * Language buttons. Changing the language recreates the activity and
     * reopens the Settings pane through the "pane" extra.
     */
    private void wireLang() {
        int cur = Lang.index(this);
        int[] ids = {R.id.lang_en, R.id.lang_ru, R.id.lang_zh};
        for (int i = 0; i < ids.length; i++) {
            Button b = settings.findViewById(ids[i]);
            boolean on = i == cur;
            b.setBackgroundTintList(null);
            b.setBackgroundResource(on ? R.drawable.tv_chip_on : R.drawable.tv_focus);
            b.setTextColor(on ? 0xFF1A1714 : 0xFFF3EDE4);
            final int idx = i;
            b.setOnClickListener(v -> {
                String tag = Lang.TAGS[idx];
                if (tag.equals(Prefs.lang(this))) {
                    return;
                }
                // apply() saves in the background; Lang.apply() switches the
                // locale now and recreate() redraws every string in it.
                Prefs.p(this).edit().putString("lang", tag).apply();
                Lang.apply(this);
                getIntent().putExtra("pane", "settings");
                recreate();
            });
        }
    }

    /**
     * Wires the Settings pane: language, the switches (each saved to Prefs as
     * soon as it is flipped), links to system VPN settings, Log and About,
     * and the update button from Channel.
     */
    private void wireSettings() {
        wireLang();
        Switch d = settings.findViewById(R.id.sw_default);
        Switch lan = settings.findViewById(R.id.sw_lan);
        Switch auto = settings.findViewById(R.id.sw_auto);
        Switch re = settings.findViewById(R.id.sw_re);
        Switch log = settings.findViewById(R.id.sw_log);
        Switch crash = settings.findViewById(R.id.sw_crash);
        d.setChecked(Prefs.defaultRoute(this));
        lan.setChecked(Prefs.lanBypass(this));
        auto.setChecked(Prefs.autoConnect(this));
        re.setChecked(Prefs.reconnect(this));
        log.setChecked(Prefs.verbose(this));
        d.setOnCheckedChangeListener((b, v) -> Prefs.p(this).edit().putBoolean("default_route", v).apply());
        lan.setOnCheckedChangeListener((b, v) -> Prefs.p(this).edit().putBoolean("lan_bypass", v).apply());
        auto.setOnCheckedChangeListener((b, v) -> Prefs.p(this).edit().putBoolean("auto", v).apply());
        re.setOnCheckedChangeListener((b, v) -> Prefs.p(this).edit().putBoolean("reconnect", v).apply());
        log.setOnCheckedChangeListener((b, v) -> Prefs.p(this).edit().putBoolean("verbose", v).apply());
        // Crash reports exist only in the GitHub build; store builds hide the row.
        if (!BuildConfig.GITHUB) {
            View row = settings.findViewById(R.id.row_crash);
            if (row != null) {
                row.setVisibility(View.GONE);
            }
        } else if (crash != null) {
            crash.setChecked(Prefs.crashReports(this));
            crash.setOnCheckedChangeListener((b, v) -> Prefs.p(this).edit().putBoolean("crash", v).apply());
        }
        settings.findViewById(R.id.open_vpn).setOnClickListener(v -> {
            try {
                startActivity(new Intent(Settings.ACTION_VPN_SETTINGS));
            } catch (Exception e) {
                Toast.makeText(this, e.getMessage(), Toast.LENGTH_SHORT).show();
            }
        });
        settings.findViewById(R.id.go_log).setOnClickListener(v ->
                startActivity(new Intent(this, LogActivity.class)));
        settings.findViewById(R.id.go_about).setOnClickListener(v -> {
            Intent i = new Intent(this, AboutActivity.class);
            startActivity(i);
        });
        // Channel wires the update button in the GitHub build (self-update);
        // the store build's Channel does nothing, the store updates the app.
        Channel.attach(this, settings);
    }

    /**
     * Looks up the public IP through the tunnel on the background pool, one
     * lookup at a time. While a lookup runs Live.extIP shows "…", and "—"
     * after a failure. The network call must stay off the main thread
     * (Android forbids network IO there and it would freeze the UI).
     *
     * <p>A failure is retried after 2.5 s, 5 s and 10 s (EXT_RETRY_MS), then
     * not again until the connection state changes (paintHome resets the
     * count). The waiting retry is cancelled in onPause and onDestroy.
     *
     * <p>The background part uses only the application context and writes
     * only Live, so it never holds this activity; if the activity is gone
     * when it finishes, the result still lands in Live (the next screen shows
     * it) and only the retry scheduling is skipped.
     */
    private void fetchExt() {
        if (!Prefs.showExtIP(this) || !Live.on()) {
            return;
        }
        if (!extFetch.compareAndSet(false, true)) {
            return;
        }
        Live.extIP = "…";
        Context app = getApplicationContext();
        String url = Prefs.extIPURL(this);
        Ui.bg(this, () -> {
            String got = "";
            try {
                got = ExtIP.lookup(app, url);
            } catch (RuntimeException ignored) {
                // Treated as a failed lookup below, so "…" never sticks.
            }
            Live.extIP = got.isEmpty() ? "—" : got;
            // One Bus event per lookup, so every screen shows the result.
            Bus.post();
            return got;
        }, (String got, Exception err) -> {
            extFetch.set(false);
            if (got != null && !got.isEmpty()) {
                extFails = 0;
                return;
            }
            // Failed: schedule the next retry, unless we already used them all.
            if (extFails < EXT_RETRY_MS.length && Live.on() && !isFinishing()) {
                if (!visible) {
                    // Hidden now: do not queue network work; onResume retries.
                    extRetryPaused = true;
                    return;
                }
                long wait = EXT_RETRY_MS[extFails++];
                Ui.MAIN.removeCallbacks(extRetry);
                Ui.MAIN.postDelayed(extRetry, wait);
                extRetryQueued = true;
            }
        });
    }

    /** Converts dp to pixels for this screen. */
    private int dp(int v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }
}
