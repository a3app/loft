package app.loft.android;

import android.app.AlertDialog;
import android.content.Intent;
import android.os.Bundle;
import android.view.View;
import android.widget.EditText;
import android.widget.PopupMenu;
import android.widget.Switch;
import android.widget.TextView;

public final class EditActivity extends LoftActivity {
    private static final String[] KINDS = {"loft", "sstp", "vless", "trojan"};
    private static final String[] METHODS = {"auto", "get", "ws", "h2", "quic", "h3", "l3"};
    private static final int REQ_SCAN = 2;

    private Profile p;
    private boolean fresh;
    private boolean manual;
    private boolean shown;
    private String linkFull = "";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_edit);
        String id = getIntent().getStringExtra("id");
        if (id != null) {
            p = LoftApp.of(this).profiles().get(id);
        }
        if (p == null) {
            p = new Profile();
            fresh = true;
        }
        findViewById(R.id.btn_close).setOnClickListener(v -> finish());
        findViewById(R.id.btn_save).setOnClickListener(v -> save());
        findViewById(R.id.btn_delete).setOnClickListener(v -> {
            if (fresh) {
                finish();
                return;
            }
            new AlertDialog.Builder(this)
                    .setMessage(R.string.confirm_delete)
                    .setPositiveButton(R.string.delete, (d, w) -> {
                        LoftApp.of(this).profiles().delete(p.id);
                        finish();
                    })
                    .setNegativeButton(R.string.cancel, null)
                    .show();
        });
        findViewById(R.id.btn_delete).setVisibility(fresh ? View.GONE : View.VISIBLE);
        findViewById(R.id.btn_more).setVisibility(fresh ? View.GONE : View.VISIBLE);
        findViewById(R.id.btn_scan).setVisibility(fresh ? View.VISIBLE : View.GONE);
        findViewById(R.id.btn_scan).setOnClickListener(v ->
                startActivityForResult(new Intent(this, ScanActivity.class).putExtra("return", true), REQ_SCAN));
        findViewById(R.id.btn_more).setOnClickListener(v -> {
            PopupMenu m = new PopupMenu(this, v);
            m.getMenu().add(0, 1, 0, R.string.share);
            m.getMenu().add(0, 2, 0, R.string.export);
            m.setOnMenuItemClickListener(item -> {
                saveQuiet();
                if (item.getItemId() == 1) {
                    startActivity(new Intent(this, ShareQrActivity.class).putExtra("id", p.id));
                } else {
                    Intent i = new Intent(Intent.ACTION_SEND);
                    i.setType("text/plain");
                    i.putExtra(Intent.EXTRA_TEXT, p.sessionYaml());
                    i.putExtra(Intent.EXTRA_SUBJECT, p.name + ".loft");
                    startActivity(Intent.createChooser(i, getString(R.string.export)));
                }
                return true;
            });
            m.show();
        });
        ((TextView) findViewById(R.id.title)).setText(fresh ? R.string.new_conn : R.string.edit_title);
        fill();
        Seg.bind(findViewById(R.id.kind_seg), index(KINDS, p.family()), i -> show());
        Seg.bind(findViewById(R.id.entry_seg), 0, i -> {
            manual = i == 1;
            show();
        });
        int mi = index(METHODS, p.isLoft() ? p.kind.toLowerCase() : "auto");
        if (mi < 0) {
            mi = 0;
        }
        Seg.bind(findViewById(R.id.method_seg), mi, i -> show());
        findViewById(R.id.kind_scroll).setVisibility(fresh ? View.VISIBLE : View.GONE);
        TextView cap = findViewById(R.id.kind_cap);
        cap.setVisibility(fresh ? View.GONE : View.VISIBLE);
        cap.setText(p.family());
        findViewById(R.id.btn_adv).setOnClickListener(v -> {
            View adv = findViewById(R.id.block_adv);
            adv.setVisibility(adv.getVisibility() == View.VISIBLE ? View.GONE : View.VISIBLE);
        });
        findViewById(R.id.btn_eye).setOnClickListener(v -> {
            shown = !shown;
            EditText link = findViewById(R.id.link);
            link.setText(shown ? linkFull : mask(linkFull));
            ((android.widget.Button) v).setText(shown ? R.string.hide_secret : R.string.show_secret);
        });
        show();
    }

    private void fill() {
        set(R.id.name, p.name);
        set(R.id.token, p.token);
        set(R.id.user, p.user);
        set(R.id.pass, p.pass);
        set(R.id.host, p.host());
        int port = YamlBuilder.portOf(p.url, YamlBuilder.schemeOf(p.url, p.kind));
        set(R.id.port, port > 0 ? String.valueOf(port) : "");
        set(R.id.path, p.path);
        linkFull = YamlBuilder.emit(p);
        set(R.id.link, mask(linkFull));
        set(R.id.dns, p.dns);
        set(R.id.sni, p.sni);
        ((Switch) findViewById(R.id.sw_mux)).setChecked(p.mux && !p.isL3());
        ((Switch) findViewById(R.id.sw_tls)).setChecked(p.tlsCheck);
        ((Switch) findViewById(R.id.sw_pap)).setChecked(p.pap);
        ((Switch) findViewById(R.id.sw_dns_sys)).setChecked(p.dnsSystem);
    }

    private void show() {
        String fam = fresh ? KINDS[Seg.selected(findViewById(R.id.kind_seg))] : p.family();
        boolean loft = "loft".equals(fam);
        boolean sstp = "sstp".equals(fam);
        boolean vless = "vless".equals(fam);
        boolean linkKinds = loft || vless || "trojan".equals(fam) || sstp;
        findViewById(R.id.entry_seg).setVisibility(loft || vless ? View.VISIBLE : View.GONE);
        findViewById(R.id.block_link).setVisibility(linkKinds && !manual ? View.VISIBLE : View.GONE);
        findViewById(R.id.block_loft_man).setVisibility((loft || vless) && manual ? View.VISIBLE : View.GONE);
        int manVis = loft && manual ? View.VISIBLE : View.GONE;
        findViewById(R.id.lbl_method).setVisibility(manVis);
        findViewById(R.id.method_row).setVisibility(manVis);
        findViewById(R.id.path).setVisibility(manVis);
        ((TextView) findViewById(R.id.lbl_token)).setText(vless ? R.string.uuid : R.string.key_title);
        findViewById(R.id.block_sstp).setVisibility(sstp || "trojan".equals(fam) ? View.VISIBLE : View.GONE);
        findViewById(R.id.user).setVisibility(sstp ? View.VISIBLE : View.GONE);
        findViewById(R.id.row_pap).setVisibility(sstp ? View.VISIBLE : View.GONE);
        findViewById(R.id.row_tls).setVisibility(sstp || vless || "trojan".equals(fam) ? View.VISIBLE : View.GONE);
        int mi = Seg.selected(findViewById(R.id.method_seg));
        findViewById(R.id.row_mux).setVisibility(loft && mi != 6 ? View.VISIBLE : View.GONE);
        TextView lbl = findViewById(R.id.lbl_link);
        if (vless) {
            lbl.setText(R.string.vless_url);
        } else if ("trojan".equals(fam)) {
            lbl.setText(R.string.trojan_url);
        } else if (sstp) {
            lbl.setText(R.string.sstp_url);
        } else {
            lbl.setText(R.string.loft_uri);
        }
        findViewById(R.id.sni).setVisibility(sstp || "trojan".equals(fam) ? View.VISIBLE : View.GONE);
    }

    private void save() {
        saveQuiet();
        finish();
    }

    private void saveQuiet() {
        String fam = fresh ? KINDS[Seg.selected(findViewById(R.id.kind_seg))] : p.family();
        p.name = text(R.id.name);
        p.dns = text(R.id.dns);
        p.dnsSystem = ((Switch) findViewById(R.id.sw_dns_sys)).isChecked();
        p.sni = text(R.id.sni);
        if ("loft".equals(fam)) {
            if (!manual) {
                String raw = shown ? text(R.id.link) : linkFull;
                if (raw.isEmpty()) {
                    raw = text(R.id.link);
                }
                java.util.List<Profile> got = Importer.parse(raw);
                if (!got.isEmpty()) {
                    Profile n = got.get(0);
                    n.id = p.id;
                    n.name = p.name.isEmpty() ? n.name : p.name;
                    n.dns = p.dns;
                    n.dnsSystem = p.dnsSystem;
                    p = n;
                    p.yaml = YamlBuilder.from(p);
                    LoftApp.of(this).profiles().put(p);
                    return;
                }
            }
            int mi = Seg.selected(findViewById(R.id.method_seg));
            String type = METHODS[mi];
            if ("auto".equals(type)) {
                type = "get";
            }
            p.kind = type;
            p.token = text(R.id.token);
            p.path = text(R.id.path);
            p.mux = ((Switch) findViewById(R.id.sw_mux)).isChecked() && !"l3".equals(type);
            String host = text(R.id.host);
            int port = num(text(R.id.port), 443);
            String sch = "ws".equals(type) ? "wss" : "https";
            p.url = sch + "://" + host + (port == 443 ? "" : ":" + port);
            if (!"get".equals(type) && !p.path.isEmpty()) {
                p.url += (p.path.startsWith("/") ? p.path : "/" + p.path);
                p.path = "";
            }
        } else if ("vless".equals(fam)) {
            p.kind = "vless";
            p.tlsCheck = ((Switch) findViewById(R.id.sw_tls)).isChecked();
            if (manual) {
                p.token = text(R.id.token);
                String host = text(R.id.host);
                int port = num(text(R.id.port), 443);
                p.url = "vless://" + p.token + "@" + host + (port == 443 ? "" : ":" + port);
            } else {
                String raw = shown ? text(R.id.link) : (linkFull.isEmpty() ? text(R.id.link) : linkFull);
                java.util.List<Profile> got = Importer.parse(raw);
                if (!got.isEmpty()) {
                    Profile n = got.get(0);
                    n.id = p.id;
                    n.name = p.name.isEmpty() ? n.name : p.name;
                    n.dns = p.dns;
                    n.dnsSystem = p.dnsSystem;
                    p = n;
                    p.yaml = YamlBuilder.from(p);
                    LoftApp.of(this).profiles().put(p);
                    return;
                }
                p.url = raw;
            }
        } else if ("sstp".equals(fam)) {
            p.kind = "sstp";
            p.user = text(R.id.user);
            p.pass = text(R.id.pass);
            p.token = p.pass;
            p.tlsCheck = ((Switch) findViewById(R.id.sw_tls)).isChecked();
            p.pap = ((Switch) findViewById(R.id.sw_pap)).isChecked();
            String raw = shown ? text(R.id.link) : (linkFull.isEmpty() ? text(R.id.link) : linkFull);
            if (raw.isEmpty()) {
                raw = text(R.id.host);
            }
            if (raw.startsWith("http://") || raw.startsWith("https://") || raw.startsWith("sstp://")) {
                p.url = raw;
            } else {
                String host = raw.isEmpty() ? hostOf(p.url) : raw;
                int port = num(text(R.id.port), 443);
                p.url = "https://" + host + (port == 443 ? "" : ":" + port);
            }
        } else {
            p.kind = fam;
            String raw = shown ? text(R.id.link) : (linkFull.isEmpty() ? text(R.id.link) : linkFull);
            p.url = raw;
            p.token = text(R.id.token);
            p.tlsCheck = ((Switch) findViewById(R.id.sw_tls)).isChecked();
        }
        if (p.name.isEmpty()) {
            p.name = p.host();
        }
        p.yaml = YamlBuilder.from(p);
        LoftApp.of(this).profiles().put(p);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQ_SCAN || resultCode != RESULT_OK || data == null) {
            return;
        }
        String raw = data.getStringExtra("raw");
        if (raw == null || raw.trim().isEmpty()) {
            return;
        }
        java.util.List<Profile> got = Importer.parse(raw);
        if (got.isEmpty()) {
            set(R.id.link, raw);
            shown = true;
            linkFull = raw;
            return;
        }
        String id = p.id;
        String name = p.name;
        p = got.get(0);
        p.id = id;
        if (!name.isEmpty()) {
            p.name = name;
        }
        fill();
        show();
    }

    private void set(int id, String s) {
        ((EditText) findViewById(id)).setText(s == null ? "" : s);
    }

    private String text(int id) {
        return ((EditText) findViewById(id)).getText().toString().trim();
    }

    private static String mask(String s) {
        if (s == null) {
            return "";
        }
        int a = s.indexOf("://");
        int at = s.indexOf('@');
        if (a < 0 || at <= a) {
            return s;
        }
        return s.substring(0, a + 3) + "••••••" + s.substring(at);
    }

    private static String hostOf(String u) {
        Profile x = new Profile();
        x.url = u;
        return x.host();
    }

    private static int num(String s, int def) {
        try {
            int n = Integer.parseInt(s);
            return n > 0 && n < 65536 ? n : def;
        } catch (Exception e) {
            return def;
        }
    }

    private static int index(String[] a, String v) {
        for (int i = 0; i < a.length; i++) {
            if (a[i].equalsIgnoreCase(v)) {
                return i;
            }
        }
        return 0;
    }
}
