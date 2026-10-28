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
    /** Request codes for the VPN consent dialog and the notification permission prompt. */
    private static final int REQ_VPN = 1;
    private static final int REQ_NOTE = 2;

    /** Container that holds the three tab views; only one is visible at a time. */
    private ViewGroup pane;
    private View home;
    private View conns;
    private View settings;
    private ConnAdapter adapter;
    /**
     * True once the notification permission was requested in this screen's life, so it is
     * asked at most once.
     */
    private boolean askedVpn;
    /** True while a public-IP lookup is running, so taps and redraws do not start a second one. */
    private final AtomicBoolean extFetch = new AtomicBoolean();

    /**
     * Shows Home first, wires the bottom tabs and the Home buttons, handles a link or file the app
     * was opened with, opens onboarding when there are no profiles yet, and auto-connects when
     * that setting is on.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        setTheme(R.style.Theme_Loft);
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);
        pane = findViewById(R.id.pane);
        home = getLayoutInflater().inflate(R.layout.pane_home, pane, false);
        pane.addView(home);
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
        handleIntent(getIntent());
        if (LoftApp.of(this).profiles().all().isEmpty()
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
        super.onPause();
    }

    /** Bus callback: VPN state changed, redraw Home and the connection list. */
    @Override
    public void onLoft() {
        paintHome();
        if (adapter != null) {
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
        }
        if (v == home) {
            paintHome();
        }
    }

    /**
     * Redraws Home from Live: button and status pill for connecting / on /
     * error / off, the info cards, rates and chart, the overlay address
     * (this device's address inside the loft network), the gateway and the
     * public IP seen through the tunnel. "Simple" mode hides the details.
     */
    private void paintHome() {
        // 1. Lamp, status pill and big button for the four states.
        ImageView lamp = home.findViewById(R.id.lamp);
        TextView pillT = home.findViewById(R.id.pill_t);
        View pillDot = home.findViewById(R.id.pill_dot);
        Button go = home.findViewById(R.id.go);
        View cards = home.findViewById(R.id.cards);
        View sparkRow = home.findViewById(R.id.spark_row);
        View iprows = home.findViewById(R.id.iprows);
        SparkView spark = home.findViewById(R.id.spark);
        Profile p = LoftApp.of(this).profiles().active();
        String name = p == null ? "Home" : (p.name.isEmpty() ? p.host() : p.name);
        boolean on = Live.on();
        boolean conn = Live.connecting();
        boolean simple = Prefs.simple(this);
        lamp.setImageResource(on ? R.drawable.ic_lamp_on : R.drawable.ic_lamp_off);
        if (conn) {
            pillT.setText(getString(R.string.pill_busy, name));
            pillDot.setBackgroundResource(R.drawable.bg_dot);
            go.setText(R.string.cancel);
            go.setBackgroundTintList(android.content.res.ColorStateList.valueOf(getColor(R.color.slate)));
            go.setTextColor(getColor(R.color.snow));
        } else if (on) {
            String t = getString(R.string.pill_on, name);
            if (Live.holding) {
                t = getString(R.string.hold) + " · " + t;
            }
            pillT.setText(t);
            pillDot.setBackgroundResource(R.drawable.bg_dot_on);
            go.setText(R.string.disconnect);
            go.setBackgroundTintList(android.content.res.ColorStateList.valueOf(getColor(R.color.danger_bg)));
            go.setTextColor(getColor(R.color.danger_fg));
        } else if (!Live.err.isEmpty()) {
            pillT.setText(Explain.display(this, Live.err));
            pillDot.setBackgroundResource(R.drawable.bg_dot_err);
            go.setText(R.string.connect);
            go.setBackgroundTintList(android.content.res.ColorStateList.valueOf(getColor(R.color.lamp)));
            go.setTextColor(getColor(R.color.ink));
        } else {
            pillT.setText(getString(R.string.pill_off_home, name));
            pillDot.setBackgroundResource(R.drawable.bg_dot);
            go.setText(R.string.connect);
            go.setBackgroundTintList(android.content.res.ColorStateList.valueOf(getColor(R.color.lamp)));
            go.setTextColor(getColor(R.color.ink));
        }
        // 2. Which detail blocks are visible, then the info cards.
        cards.setVisibility(simple ? View.GONE : View.VISIBLE);
        sparkRow.setVisibility(simple || (!on && !conn) ? View.GONE : View.VISIBLE);
        iprows.setVisibility(simple || !on ? View.GONE : View.VISIBLE);
        paintCards(p, on);
        // 3. Current speed and the traffic chart (a handshake animation while connecting).
        boolean bits = Prefs.rateBits(this);
        TextView rateDn = home.findViewById(R.id.rate_dn);
        TextView rateUp = home.findViewById(R.id.rate_up);
        View sparkR = home.findViewById(R.id.spark_r);
        if (on && !conn) {
            rateDn.setText("↓ " + Cidr.fmt(Live.rateDown, bits));
            rateUp.setText("↑ " + Cidr.fmt(Live.rateUp, bits));
            sparkR.setVisibility(View.VISIBLE);
        } else {
            sparkR.setVisibility(View.GONE);
        }
        spark.setBars(Prefs.sparkBars(this));
        if (conn) {
            spark.setHandshake(true, getString(R.string.handshake));
        } else if (on) {
            spark.setHandshake(false, "");
            spark.setSamples(Live.sparkDn, Live.sparkUp, Live.sparkCount);
        } else {
            spark.clear();
        }
        // 4. Public IP (fetched on demand), overlay address(es) and gateway.
        View extRow = home.findViewById(R.id.row_ext);
        boolean showExt = Prefs.showExtIP(this);
        ((View) extRow.getParent()).setVisibility(showExt ? View.VISIBLE : View.GONE);
        if (on && showExt && Live.extIP.isEmpty()) {
            fetchExt();
        }
        String ext = Live.extIP;
        ((TextView) extRow).setText(ext.isEmpty() ? "—" : ext);
        String ov = Live.ip;
        if (!Live.ip6.isEmpty()) {
            ov = ov.isEmpty() ? Live.ip6 : ov + "  " + Live.ip6;
        }
        ((TextView) home.findViewById(R.id.row_overlay)).setText(ov.isEmpty() ? "—" : ov);
        ((TextView) home.findViewById(R.id.row_route)).setText(Live.gateway.isEmpty() ? "—" : Live.gateway);
    }

    /**
     * Fills the three info cards for the protocol family. For loft they show the
     * front in use and the options: mux (many streams in one connection) or IP
     * for L3, "mask" when the node's mask key is known, and "hold" while the
     * session waits on a network switch.
     */
    private void paintCards(Profile p, boolean on) {
        String fam = p == null ? "loft" : p.family();
        TextView k1 = home.findViewById(R.id.card_k1);
        TextView k2 = home.findViewById(R.id.card_k2);
        TextView k3 = home.findViewById(R.id.card_k3);
        TextView v1 = home.findViewById(R.id.card_v1);
        TextView v2 = home.findViewById(R.id.card_v2);
        TextView v3 = home.findViewById(R.id.card_v3);
        k1.setText(R.string.proto_k);
        if ("sstp".equals(fam)) {
            k2.setText(R.string.trans_k);
            k3.setText(R.string.auth_k);
            v1.setText("SSTP");
            v2.setText("PPP");
            v3.setText("CHAP");
        } else if ("vless".equals(fam)) {
            k2.setText(R.string.trans_k);
            k3.setText(R.string.sec_k);
            v1.setText("VLESS");
            v2.setText(p == null ? "TCP" : p.linkTransport());
            v3.setText(p == null ? "TLS" : p.linkSecurity());
        } else if ("trojan".equals(fam)) {
            k2.setText(R.string.trans_k);
            k3.setText(R.string.sec_k);
            v1.setText("Trojan");
            v2.setText(p == null ? "TCP" : p.linkTransport());
            v3.setText(p == null ? "TLS" : p.linkSecurity());
        } else {
            k2.setText(R.string.front_k);
            k3.setText(R.string.opt_k);
            v1.setText("loft/1.0");
            String front = Live.kind.isEmpty() && p != null ? p.kind : Live.kind;
            if (!on && (front.isEmpty() || "get".equalsIgnoreCase(front))) {
                v2.setText(R.string.kind_auto_v);
            } else {
                v2.setText(front.isEmpty() ? "—" : front.toUpperCase());
            }
            if (!on) {
                v3.setText("—");
            } else {
                String opt = "l3".equalsIgnoreCase(front) ? "IP" : getString(R.string.mux);
                if (p != null && p.maskPub != null && !p.maskPub.isEmpty()) {
                    opt = opt + " · " + getString(R.string.mask);
                }
                if (Live.holding) {
                    opt = getString(R.string.hold) + " · " + opt;
                }
                v3.setText(opt);
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
     */
    private void connect() {
        if (LoftApp.of(this).profiles().active() == null) {
            ((BottomNavigationView) findViewById(R.id.nav)).setSelectedItemId(R.id.nav_conns);
            return;
        }
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED && !askedVpn) {
            askedVpn = true;
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, REQ_NOTE);
        }
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            startActivityForResult(prep, REQ_VPN);
            return;
        }
        LoftVpnService.requestStart(this);
    }

    /** Result of the VPN consent dialog: start the service if the user allowed it. */
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

    // Export: this profile as YAML, as a QR, or every profile in one file.
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

    /** "Add connection" sheet: scan a QR, paste, open a file, or enter by hand. */
    private void addSheet() {
        BottomSheetDialog d = Sheets.of(this, R.layout.sheet_add);
        d.findViewById(R.id.add_scan).setOnClickListener(v -> {
            d.dismiss();
            startActivity(new Intent(this, ScanActivity.class));
        });
        d.findViewById(R.id.add_paste).setOnClickListener(v -> {
            d.dismiss();
            paste();
        });
        d.findViewById(R.id.add_file).setOnClickListener(v -> {
            d.dismiss();
            startActivity(new Intent(this, ScanActivity.class).putExtra("file", true));
        });
        d.findViewById(R.id.add_manual).setOnClickListener(v -> {
            d.dismiss();
            startActivity(new Intent(this, EditActivity.class));
        });
        d.show();
    }

    /** Imports from the clipboard; with an empty clipboard opens the scanner instead. */
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

    /** Imports from a loft:// link, text shared to the app, or an opened file. */
    private void handleIntent(Intent intent) {
        if (intent == null) {
            return;
        }
        Uri u = intent.getData();
        if (u != null && "loft".equalsIgnoreCase(u.getScheme())) {
            importRaw(u.toString());
            intent.setData(null);
            return;
        }
        if (Intent.ACTION_SEND.equals(intent.getAction())) {
            String t = intent.getStringExtra(Intent.EXTRA_TEXT);
            if (t != null) {
                importRaw(t);
            }
            return;
        }
        if (u != null && Intent.ACTION_VIEW.equals(intent.getAction())) {
            try (InputStream in = getContentResolver().openInputStream(u)) {
                if (in != null) {
                    importRaw(readAll(in));
                }
            } catch (Exception ignored) {
            }
        }
    }

    /** Adds every profile found in the text and switches to the Connections tab. */
    void importRaw(String raw) {
        List<Profile> got = Importer.parse(raw);
        if (got.isEmpty()) {
            Toast.makeText(this, R.string.paste_hint, Toast.LENGTH_SHORT).show();
            return;
        }
        LoftApp.of(this).profiles().addAll(got);
        ensureConns();
        adapter.reload();
        Prefs.p(this).edit().putBoolean("onboard_done", true).apply();
        Toast.makeText(this, R.string.imported, Toast.LENGTH_SHORT).show();
        ((BottomNavigationView) findViewById(R.id.nav)).setSelectedItemId(R.id.nav_conns);
    }

    /**
     * Binds the Settings tab to Prefs. Routing switches rebuild the live TUN.
     * A language change recreates the screen.
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
        ext.setOnCheckedChangeListener((v, on) -> {
            Prefs.p(this).edit().putBoolean("ext_ip", on).apply();
            Live.extIP = "";
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
        dns.setOnItemSelectedListener(new SimplePick(i -> {
            String m = i == 2 ? "doh" : i == 1 ? "ip" : "sys";
            Prefs.p(this).edit().putString("local_dns", m).apply();
            paintDnsRow(i, dnsAddr);
        }));
        dnsAddr.addTextChangedListener(new android.text.TextWatcher() {
            @Override
            public void beforeTextChanged(CharSequence s, int a, int b, int c) {
            }

            @Override
            public void onTextChanged(CharSequence s, int a, int b, int c) {
            }

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
        Channel.attach(this, settings);
        Fit.buttonRow(settings.findViewById(R.id.settings_links));
    }

    /**
     * Looks up the public IP through the tunnel on a background thread, one
     * lookup at a time. On failure it retries every 2.5 s while the session is
     * on, since the first request may run before the tunnel carries traffic.
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

    /** Reads a whole stream as UTF-8 text. Also used by ScanActivity for opened files. */
    static String readAll(InputStream in) throws Exception {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        byte[] b = new byte[4096];
        int n;
        while ((n = in.read(b)) > 0) {
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
        private final Seg.Pick pick;
        private boolean primed;

        SimplePick(Seg.Pick pick) {
            this.pick = pick;
        }

        @Override
        public void onItemSelected(android.widget.AdapterView<?> parent, View view, int position, long id) {
            if (!primed) {
                primed = true;
                return;
            }
            pick.on(position);
        }

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
        private List<Profile> data = new ArrayList<>();
        private String openId;
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
                @Override
                public void onRevealStart(RevealFrame row) {
                    closeOthers(h);
                }

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
            final RevealFrame swipe;
            final View card;
            final View actEdit;
            final View actDel;
            final Button mini;
            final View body;
            final TextView title;
            final TextView sub;
            final TextView live;
            final TextView kind;

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
