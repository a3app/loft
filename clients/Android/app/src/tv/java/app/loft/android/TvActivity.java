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
import android.content.Intent;
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
    private View overlay;
    // selected: id of the connection card in focus, the target of edit / share /
    // delete. overlay: the full-screen panel (pairing QR, share, paste, edit)
    // shown over the current pane; Back closes it.
    private String selected = "";
    /** Running pairing listener while the pairing QR is shown, else null. */
    private TvPair pair;
    /** True while a public-IP lookup is in flight, so only one runs at a time. */
    private final java.util.concurrent.atomic.AtomicBoolean extFetch = new java.util.concurrent.atomic.AtomicBoolean();

    /**
     * Builds the rail and Home pane, opens Settings instead when asked by the
     * "pane" extra (used after a language change), imports a loft:// link the
     * app was opened with, and auto-connects when that setting is on. The
     * other panes are built right after the first frame.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        setTheme(R.style.Theme_Loft_Tv);
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_tv);
        pane = findViewById(R.id.pane);
        home = getLayoutInflater().inflate(R.layout.pane_tv_home, pane, false);
        pane.addView(home);

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
        Bus.watch(this);
        paintHome();
        paintConns();
    }

    /** Stops listening to Bus while the screen is hidden. */
    @Override
    protected void onPause() {
        Bus.drop(this);
        super.onPause();
    }

    /** Makes sure the pairing listener does not outlive the screen. */
    @Override
    protected void onDestroy() {
        stopPair();
        super.onDestroy();
    }

    /** Bus callback: connection state or stats in Live changed, so redraw. */
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
        if (v == conns) {
            paintConns();
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

    /** Colors the active rail icon amber and the others grey. */
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
     * replaced by three add tiles.
     */
    private void paintHome() {
        // 1. Status line and connect button text from the connection state.
        Profile p = LoftApp.of(this).profiles().active();
        String name = p == null ? "" : p.name;
        boolean on = Live.on();
        boolean conn = Live.connecting();
        ImageView lamp = home.findViewById(R.id.lamp);
        lamp.setImageResource(on ? R.drawable.ic_lamp_on : R.drawable.ic_lamp_off);
        TextView status = home.findViewById(R.id.status);
        Button go = home.findViewById(R.id.go);
        if (conn) {
            status.setText(getString(R.string.connecting) + (name.isEmpty() ? "" : " " + name));
            go.setText(R.string.cancel);
        } else if (on) {
            status.setText(getString(R.string.connected) + (name.isEmpty() ? "" : " · " + name)
                    + uptime());
            go.setText(R.string.disconnect);
        } else if (!Live.err.isEmpty()) {
            status.setText(Explain.display(this, Live.err));
            go.setText(R.string.connect);
        } else {
            status.setText(getString(R.string.pill_off) + (name.isEmpty() ? "" : " · " + name));
            go.setText(R.string.connect);
        }
        // 2. Side panel: the active profile, then live addresses and rates.
        TextView sn = home.findViewById(R.id.side_name);
        TextView sk = home.findViewById(R.id.side_kind);
        TextView sh = home.findViewById(R.id.side_host);
        TextView sip = home.findViewById(R.id.side_ip);
        TextView se = home.findViewById(R.id.side_ext);
        TextView snet = home.findViewById(R.id.side_net);
        TextView sr = home.findViewById(R.id.side_rate);
        if (p == null) {
            sn.setText(R.string.empty_conns);
            sk.setText("");
            sh.setText("");
        } else {
            sn.setText(p.name.isEmpty() ? p.host() : p.name);
            sk.setText(p.side());
            sh.setText(p.host());
        }
        String ov = Live.ip.isEmpty() ? "—" : Live.ip;
        if (!Live.ip6.isEmpty()) {
            ov = Live.ip.isEmpty() ? Live.ip6 : Live.ip + "  " + Live.ip6;
        }
        sip.setText(getString(R.string.overlay_ip) + "  " + ov);
        se.setText(getString(R.string.ext_ip) + "  " + (Live.extIP.isEmpty() ? "—" : Live.extIP));
        String net = Live.underlying.isEmpty() ? "—" : Live.underlying;
        snet.setText(net);
        boolean bits = Prefs.rateBits(this);
        sr.setText("↓ " + Cidr.fmt(Live.rateDown, bits) + "   ↑ " + Cidr.fmt(Live.rateUp, bits));
        // 3. Live rows only while connected; fetch the public IP if missing.
        sip.setVisibility(on ? View.VISIBLE : View.GONE);
        boolean showExt = Prefs.showExtIP(this);
        se.setVisibility(on && showExt ? View.VISIBLE : View.GONE);
        snet.setVisibility(on ? View.VISIBLE : View.GONE);
        sr.setVisibility(on ? View.VISIBLE : View.GONE);
        if (on && showExt && Live.extIP.isEmpty()) {
            fetchExt();
        }
        // 4. Nothing to show at all: hide the panel and offer add tiles.
        View side = home.findViewById(R.id.side);
        if (p == null && !on) {
            side.setVisibility(View.GONE);
        } else {
            side.setVisibility(View.VISIBLE);
        }
        if (p == null && !on && !conn) {
            go.setVisibility(View.GONE);
            showEmptyAdd();
        } else {
            go.setVisibility(View.VISIBLE);
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
            emptyAdd.setVisibility(View.VISIBLE);
            return;
        }
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.HORIZONTAL);
        box.addView(addTile(getString(R.string.add_qr), this::showPair));
        box.addView(addTile(getString(R.string.add_usb), this::pickFile));
        box.addView(addTile(getString(R.string.add_type), this::showPaste));
        LinearLayout col = (LinearLayout) ((View) home.findViewById(R.id.go)).getParent();
        int i = col.indexOfChild(home.findViewById(R.id.go));
        col.addView(box, i);
        emptyAdd = box;
    }

    /** Hides the add tiles once a profile exists or a session is up. */
    private void hideEmptyAdd() {
        if (emptyAdd != null) {
            emptyAdd.setVisibility(View.GONE);
        }
    }

    /** One large focusable tile button that runs {@code run} when pressed. */
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

    /** Rebuilds the row of connection cards plus a "+" card, and focuses the first card. */
    private void paintConns() {
        if (conns == null) {
            return;
        }
        LinearLayout row = conns.findViewById(R.id.row);
        row.removeAllViews();
        List<Profile> all = LoftApp.of(this).profiles().all();
        String active = LoftApp.of(this).profiles().activeId();
        if (selected.isEmpty() && !active.isEmpty()) {
            selected = active;
        }
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
        plus.requestFocus();
        if (!all.isEmpty()) {
            row.getChildAt(0).requestFocus();
        }
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
        c.setOnClickListener(v -> {
            LoftApp.of(this).profiles().setActive(p.id);
            selected = p.id;
            show(home);
        });
        c.setOnFocusChangeListener((v, has) -> {
            if (has) {
                selected = p.id;
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
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setBackgroundColor(0xFF1A1714);
        box.setPadding(dp(32), dp(24), dp(32), dp(24));
        box.setGravity(Gravity.CENTER_HORIZONTAL);
        ImageView qr = new ImageView(this);
        qr.setImageBitmap(Qr.encode(pair.url, 512));
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

    /** Shows the focused connection as a share link and QR, with a warning that it contains the key. */
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
        qr.setImageBitmap(Qr.encode(uri, 512));
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

    /** Overlay with a text field for typing or pasting a link, and an Add button. */
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
     * Small TV editor: name and, for loft, the front. The "Auto" chip stores
     * GET because the config already uses "select: auto".
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
                    paintHome();
                })
                .setNegativeButton(R.string.cancel, null)
                .show();
    }

    /** Adds every profile found in the text, closes pairing and shows Connections. */
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

    /** Connect button: stops a running or starting session, otherwise connects. */
    private void toggle() {
        if (Live.on() || Live.connecting() || LoftVpnService.running) {
            LoftVpnService.requestStop(this);
            return;
        }
        connect();
    }

    /** Starts the active profile, asking for VPN consent first when the system needs it. */
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

    /**
     * VPN consent granted: start the session. File picked: read up to
     * 256 KiB of it and import the profiles inside.
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
            try (InputStream in = getContentResolver().openInputStream(u)) {
                if (in != null) {
                    byte[] buf = new byte[256 * 1024];
                    int n = in.read(buf);
                    if (n > 0) {
                        importRaw(new String(buf, 0, n, StandardCharsets.UTF_8));
                    }
                }
            } catch (Exception e) {
                Toast.makeText(this, e.getMessage(), Toast.LENGTH_SHORT).show();
            }
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
        Channel.attach(this, settings);
    }

    /**
     * Looks up the public IP through the tunnel on a background thread, one
     * lookup at a time; retries every 2.5 s while the session is on.
     */
    private void fetchExt() {
        if (!Prefs.showExtIP(this) || !Live.on()) {
            return;
        }
        if (!extFetch.compareAndSet(false, true)) {
            return;
        }
        Live.extIP = "…";
        new Thread(() -> {
            String got = ExtIP.lookup(this, Prefs.extIPURL(this));
            if (!got.isEmpty()) {
                Live.extIP = got;
                extFetch.set(false);
                Bus.post();
                return;
            }
            Live.extIP = "—";
            extFetch.set(false);
            Bus.post();
            View v = home;
            if (v != null) {
                v.postDelayed(() -> {
                    if (Live.on() && Prefs.showExtIP(this)
                            && ("—".equals(Live.extIP) || Live.extIP.isEmpty() || "…".equals(Live.extIP))) {
                        Live.extIP = "";
                        fetchExt();
                    }
                }, 2500);
            }
        }, "loft-extip").start();
    }

    /** Converts dp to pixels for this screen. */
    private int dp(int v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }
}
