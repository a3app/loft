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
import android.view.LayoutInflater;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.Button;
import android.widget.ImageView;
import android.widget.ListView;
import android.widget.Switch;
import android.widget.TextView;
import android.widget.Toast;

import androidx.appcompat.app.AppCompatActivity;

import com.google.android.material.bottomnavigation.BottomNavigationView;
import com.google.android.material.floatingactionbutton.FloatingActionButton;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;

public final class MainActivity extends AppCompatActivity implements Bus.Listener {
    private static final int REQ_VPN = 1;
    private static final int REQ_NOTE = 2;

    private View home;
    private View conns;
    private View settings;
    private ConnAdapter adapter;
    private boolean askedVpn;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);
        LayoutInflater inf = getLayoutInflater();
        ViewGroup pane = findViewById(R.id.pane);
        home = inf.inflate(R.layout.pane_home, pane, false);
        conns = inf.inflate(R.layout.pane_conns, pane, false);
        settings = inf.inflate(R.layout.pane_settings, pane, false);
        pane.addView(home);
        pane.addView(conns);
        pane.addView(settings);
        show(home);

        BottomNavigationView nav = findViewById(R.id.nav);
        nav.setOnItemSelectedListener(item -> {
            int id = item.getItemId();
            if (id == R.id.nav_home) {
                show(home);
            } else if (id == R.id.nav_conns) {
                show(conns);
            } else {
                show(settings);
            }
            return true;
        });

        home.findViewById(R.id.go).setOnClickListener(v -> toggle());
        home.findViewById(R.id.pill).setOnClickListener(v -> {
            nav.setSelectedItemId(R.id.nav_conns);
        });

        adapter = new ConnAdapter();
        ListView list = conns.findViewById(R.id.list);
        list.setAdapter(adapter);
        list.setOnItemClickListener((p, v, pos, id) -> {
            Profile pr = adapter.getItem(pos);
            if (pr != null) {
                startActivity(new Intent(this, EditActivity.class).putExtra("id", pr.id));
            }
        });
        list.setOnItemLongClickListener((p, v, pos, id) -> {
            Profile pr = adapter.getItem(pos);
            if (pr == null) {
                return true;
            }
            new AlertDialog.Builder(this)
                    .setItems(new CharSequence[]{
                            getString(R.string.edit),
                            getString(R.string.share),
                            getString(R.string.export),
                            getString(R.string.delete)
                    }, (d, w) -> {
                        if (w == 0) {
                            startActivity(new Intent(this, EditActivity.class).putExtra("id", pr.id));
                        } else if (w == 1) {
                            startActivity(new Intent(this, ShareQrActivity.class).putExtra("id", pr.id));
                        } else if (w == 2) {
                            Intent i = new Intent(Intent.ACTION_SEND);
                            i.setType("text/plain");
                            i.putExtra(Intent.EXTRA_TEXT, pr.sessionYaml());
                            i.putExtra(Intent.EXTRA_SUBJECT, pr.name + ".loft");
                            startActivity(Intent.createChooser(i, getString(R.string.export)));
                        } else {
                            new AlertDialog.Builder(this)
                                    .setMessage(R.string.confirm_delete)
                                    .setPositiveButton(R.string.delete, (a, b) -> {
                                        LoftApp.of(this).profiles().delete(pr.id);
                                        adapter.reload();
                                    })
                                    .setNegativeButton(R.string.cancel, null)
                                    .show();
                        }
                    })
                    .show();
            return true;
        });
        FloatingActionButton fab = conns.findViewById(R.id.fab);
        fab.setOnClickListener(v -> addSheet());

        wireSettings();
        handleIntent(getIntent());
        if (Prefs.autoConnect(this) && LoftApp.of(this).profiles().active() != null
                && !LoftVpnService.running && !Live.on()) {
            connect();
        }
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        handleIntent(intent);
    }

    @Override
    protected void onResume() {
        super.onResume();
        Bus.watch(this);
        adapter.reload();
        onLoft();
    }

    @Override
    protected void onPause() {
        Bus.drop(this);
        super.onPause();
    }

    @Override
    public void onLoft() {
        paintHome();
        adapter.notifyDataSetChanged();
    }

    private void show(View v) {
        home.setVisibility(v == home ? View.VISIBLE : View.GONE);
        conns.setVisibility(v == conns ? View.VISIBLE : View.GONE);
        settings.setVisibility(v == settings ? View.VISIBLE : View.GONE);
        if (v == conns) {
            adapter.reload();
        }
    }

    private void paintHome() {
        ImageView lamp = home.findViewById(R.id.lamp);
        TextView pill = home.findViewById(R.id.pill);
        Button go = home.findViewById(R.id.go);
        View cards = home.findViewById(R.id.cards);
        Profile p = LoftApp.of(this).profiles().active();
        String name = p == null ? "" : p.name;
        boolean on = Live.on();
        boolean conn = Live.connecting();
        lamp.setImageResource(on ? R.drawable.ic_lamp_on : R.drawable.ic_lamp_off);
        if (conn) {
            pill.setText(getString(R.string.connecting) + (name.isEmpty() ? "" : " " + name));
            go.setText(R.string.cancel);
        } else if (on) {
            String h = Live.holding ? getString(R.string.hold) + " · " : "";
            pill.setText(h + getString(R.string.connected) + (name.isEmpty() ? "" : " · " + name));
            go.setText(R.string.disconnect);
        } else {
            pill.setText(getString(R.string.pill_off) + (name.isEmpty() ? "" : " · " + name));
            go.setText(R.string.connect);
        }
        cards.setVisibility(Prefs.simple(this) ? View.GONE : View.VISIBLE);
        String kind = Live.kind.isEmpty() && p != null ? p.kind : Live.kind;
        ((TextView) home.findViewById(R.id.card_proto)).setText(
                getString(R.string.protocol) + "  loft/1.0");
        ((TextView) home.findViewById(R.id.card_front)).setText(
                getString(R.string.front) + "  " + (kind.isEmpty() ? "—" : kind.toUpperCase()));
        String opt = "l3".equalsIgnoreCase(kind) ? "IP" : getString(R.string.mux);
        if (Live.holding) {
            opt = getString(R.string.hold) + " · " + opt;
        }
        ((TextView) home.findViewById(R.id.card_opt)).setText(
                getString(R.string.options) + "  " + opt);
        boolean bits = Prefs.rateBits(this);
        ((TextView) home.findViewById(R.id.speeds)).setText(
                "↓ " + Cidr.fmt(Live.rateDown, bits) + "   ↑ " + Cidr.fmt(Live.rateUp, bits));
        String ext = Live.extIP.isEmpty() ? "—" : Live.extIP;
        ((TextView) home.findViewById(R.id.row_ext)).setText(getString(R.string.ext_ip) + "  " + ext);
        String ov = Live.ip;
        if (!Live.ip6.isEmpty()) {
            ov = ov.isEmpty() ? Live.ip6 : ov + "  " + Live.ip6;
        }
        ((TextView) home.findViewById(R.id.row_overlay)).setText(
                getString(R.string.overlay_ip) + "  " + (ov.isEmpty() ? "—" : ov));
        ((TextView) home.findViewById(R.id.row_route)).setText(
                getString(R.string.route) + "  " + (Live.gateway.isEmpty() ? "—" : Live.gateway));
        ((TextView) home.findViewById(R.id.row_net)).setText(
                getString(R.string.underlying) + "  " + (Live.underlying.isEmpty() ? "—" : Live.underlying));
        if (on && Prefs.showExtIP(this) && Live.extIP.isEmpty()) {
            fetchExt();
        }
    }

    private void toggle() {
        if (Live.on() || Live.connecting() || LoftVpnService.running) {
            LoftVpnService.requestStop(this);
            return;
        }
        connect();
    }

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

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_VPN && resultCode == Activity.RESULT_OK) {
            LoftVpnService.requestStart(this);
        }
    }

    private void addSheet() {
        String[] items = {
                getString(R.string.scan),
                getString(R.string.paste),
                getString(R.string.from_file),
                getString(R.string.manual)
        };
        new AlertDialog.Builder(this)
                .setItems(items, (d, which) -> {
                    if (which == 0) {
                        startActivity(new Intent(this, ScanActivity.class));
                    } else if (which == 1) {
                        paste();
                    } else if (which == 2) {
                        startActivity(new Intent(this, ScanActivity.class).putExtra("file", true));
                    } else {
                        startActivity(new Intent(this, EditActivity.class));
                    }
                })
                .show();
    }

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

    void importRaw(String raw) {
        List<Profile> got = Importer.parse(raw);
        if (got.isEmpty()) {
            Toast.makeText(this, R.string.paste_hint, Toast.LENGTH_SHORT).show();
            return;
        }
        LoftApp.of(this).profiles().addAll(got);
        adapter.reload();
        Toast.makeText(this, R.string.imported, Toast.LENGTH_SHORT).show();
        ((BottomNavigationView) findViewById(R.id.nav)).setSelectedItemId(R.id.nav_conns);
    }

    private void wireSettings() {
        Switch simple = settings.findViewById(R.id.sw_simple);
        Switch bits = settings.findViewById(R.id.sw_bits);
        Switch def = settings.findViewById(R.id.sw_default);
        Switch lan = settings.findViewById(R.id.sw_lan);
        Switch split = settings.findViewById(R.id.sw_split);
        Switch auto = settings.findViewById(R.id.sw_auto);
        Switch re = settings.findViewById(R.id.sw_re);
        Switch ext = settings.findViewById(R.id.sw_ext);
        simple.setChecked(Prefs.simple(this));
        bits.setChecked(Prefs.rateBits(this));
        def.setChecked(Prefs.defaultRoute(this));
        lan.setChecked(Prefs.lanBypass(this));
        split.setChecked(Prefs.split(this));
        auto.setChecked(Prefs.autoConnect(this));
        re.setChecked(Prefs.reconnect(this));
        ext.setChecked(Prefs.showExtIP(this));
        simple.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("simple", on).apply());
        bits.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("rate_bits", on).apply());
        def.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("default_route", on).apply());
        lan.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("lan_bypass", on).apply());
        split.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("split", on).apply());
        auto.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("auto", on).apply());
        re.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("reconnect", on).apply());
        ext.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("ext_ip", on).apply());
        settings.findViewById(R.id.btn_vpn).setOnClickListener(v ->
                startActivity(new Intent(Settings.ACTION_VPN_SETTINGS)));
        settings.findViewById(R.id.btn_log).setOnClickListener(v ->
                startActivity(new Intent(this, LogActivity.class)));
        settings.findViewById(R.id.btn_about).setOnClickListener(v ->
                startActivity(new Intent(this, AboutActivity.class)));
        settings.findViewById(R.id.btn_routes).setOnClickListener(v ->
                startActivity(new Intent(this, RoutesActivity.class)));
    }

    private void fetchExt() {
        Live.extIP = "…";
        new Thread(() -> {
            try {
                java.net.URL u = new java.net.URL(Prefs.extIPURL(this));
                java.net.HttpURLConnection c = (java.net.HttpURLConnection) u.openConnection();
                c.setConnectTimeout(4000);
                c.setReadTimeout(4000);
                try (InputStream in = c.getInputStream()) {
                    String s = readAll(in).trim();
                    if (s.length() > 64) {
                        s = s.substring(0, 64);
                    }
                    Live.extIP = s;
                }
            } catch (Exception e) {
                Live.extIP = "";
            }
            Bus.post();
        }).start();
    }

    static String readAll(InputStream in) throws Exception {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        byte[] b = new byte[4096];
        int n;
        while ((n = in.read(b)) > 0) {
            o.write(b, 0, n);
        }
        return o.toString(StandardCharsets.UTF_8.name());
    }

    private final class ConnAdapter extends BaseAdapter {
        private List<Profile> data = new ArrayList<>();

        void reload() {
            data = LoftApp.of(MainActivity.this).profiles().all();
            notifyDataSetChanged();
        }

        @Override
        public int getCount() {
            return data.size();
        }

        @Override
        public Profile getItem(int i) {
            return data.get(i);
        }

        @Override
        public long getItemId(int i) {
            return i;
        }

        @Override
        public View getView(int i, View v, ViewGroup parent) {
            if (v == null) {
                v = getLayoutInflater().inflate(R.layout.row_conn, parent, false);
            }
            Profile p = data.get(i);
            boolean active = p.id.equals(LoftApp.of(MainActivity.this).profiles().activeId());
            ((TextView) v.findViewById(R.id.title)).setText(p.name.isEmpty() ? p.host() : p.name);
            String sub = p.side() + " · " + p.host();
            if (active) {
                sub = sub + " · " + getString(R.string.active);
            }
            ((TextView) v.findViewById(R.id.sub)).setText(sub);
            Button mini = v.findViewById(R.id.mini);
            boolean thisUp = active && (Live.on() || Live.connecting());
            mini.setText(thisUp ? R.string.disconnect : R.string.connect);
            mini.setOnClickListener(x -> {
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
            return v;
        }
    }
}
