package app.loft.android;

import android.app.AlertDialog;
import android.content.Intent;
import android.os.Bundle;
import android.view.View;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.EditText;
import android.widget.Spinner;
import android.widget.Switch;
import android.widget.TextView;

import androidx.appcompat.app.AppCompatActivity;

public final class EditActivity extends AppCompatActivity {
    private static final String[] KINDS = {"loft", "sstp", "vless", "trojan"};
    private static final String[] METHODS = {"auto", "get", "ws", "h2", "quic", "h3", "l3"};
    private static final String[] METHOD_TITLES = {"Auto", "GET", "WS", "H2", "QUIC", "H3", "L3"};

    private Profile p;
    private boolean fresh;
    private Spinner kind;
    private Spinner method;

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
        kind = findViewById(R.id.kind);
        method = findViewById(R.id.method);
        kind.setAdapter(new ArrayAdapter<>(this, android.R.layout.simple_spinner_dropdown_item, KINDS));
        method.setAdapter(new ArrayAdapter<>(this, android.R.layout.simple_spinner_dropdown_item, METHOD_TITLES));
        kind.setEnabled(fresh);
        fill();
        kind.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener() {
            @Override
            public void onItemSelected(AdapterView<?> parent, View view, int position, long id) {
                show();
            }

            @Override
            public void onNothingSelected(AdapterView<?> parent) {
            }
        });
        method.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener() {
            @Override
            public void onItemSelected(AdapterView<?> parent, View view, int position, long id) {
                findViewById(R.id.row_mux).setVisibility(
                        "l3".equals(METHODS[position]) ? View.GONE : View.VISIBLE);
            }

            @Override
            public void onNothingSelected(AdapterView<?> parent) {
            }
        });
        findViewById(R.id.btn_close).setOnClickListener(v -> finish());
        findViewById(R.id.btn_save).setOnClickListener(v -> save());
        findViewById(R.id.btn_share).setOnClickListener(v -> {
            saveQuiet();
            startActivity(new Intent(this, ShareQrActivity.class).putExtra("id", p.id));
        });
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
        ((TextView) findViewById(R.id.title)).setText(fresh ? R.string.new_conn : R.string.edit);
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
        set(R.id.link, p.url);
        ((Switch) findViewById(R.id.sw_mux)).setChecked(p.mux && !p.isL3());
        ((Switch) findViewById(R.id.sw_tls)).setChecked(p.tlsCheck);
        ((Switch) findViewById(R.id.sw_pap)).setChecked(p.pap);
        kind.setSelection(index(KINDS, p.family()));
        int mi = index(METHODS, p.kind.toLowerCase());
        method.setSelection(mi < 0 ? 0 : mi);
    }

    private void show() {
        String fam = KINDS[kind.getSelectedItemPosition()];
        boolean loft = "loft".equals(fam);
        boolean sstp = "sstp".equals(fam);
        boolean link = "vless".equals(fam) || "trojan".equals(fam);
        findViewById(R.id.token).setVisibility(loft || link ? View.VISIBLE : View.GONE);
        findViewById(R.id.user).setVisibility(sstp ? View.VISIBLE : View.GONE);
        findViewById(R.id.pass).setVisibility(sstp ? View.VISIBLE : View.GONE);
        findViewById(R.id.host).setVisibility(loft || sstp ? View.VISIBLE : View.GONE);
        findViewById(R.id.port).setVisibility(loft || sstp ? View.VISIBLE : View.GONE);
        findViewById(R.id.lbl_method).setVisibility(loft ? View.VISIBLE : View.GONE);
        findViewById(R.id.method).setVisibility(loft ? View.VISIBLE : View.GONE);
        findViewById(R.id.path).setVisibility(loft ? View.VISIBLE : View.GONE);
        findViewById(R.id.link).setVisibility(link ? View.VISIBLE : View.GONE);
        findViewById(R.id.row_mux).setVisibility(loft && method.getSelectedItemPosition() != 6 ? View.VISIBLE : View.GONE);
        findViewById(R.id.row_tls).setVisibility(sstp || link ? View.VISIBLE : View.GONE);
        findViewById(R.id.row_pap).setVisibility(sstp ? View.VISIBLE : View.GONE);
    }

    private void save() {
        saveQuiet();
        finish();
    }

    private void saveQuiet() {
        String fam = KINDS[kind.getSelectedItemPosition()];
        p.name = text(R.id.name);
        if ("loft".equals(fam)) {
            int mi = method.getSelectedItemPosition();
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
        } else if ("sstp".equals(fam)) {
            p.kind = "sstp";
            p.user = text(R.id.user);
            p.pass = text(R.id.pass);
            p.token = p.pass;
            p.tlsCheck = ((Switch) findViewById(R.id.sw_tls)).isChecked();
            p.pap = ((Switch) findViewById(R.id.sw_pap)).isChecked();
            String host = text(R.id.host);
            int port = num(text(R.id.port), 443);
            p.url = "https://" + host + (port == 443 ? "" : ":" + port);
        } else {
            p.kind = fam;
            p.url = text(R.id.link);
            p.token = text(R.id.token);
            p.tlsCheck = ((Switch) findViewById(R.id.sw_tls)).isChecked();
        }
        if (p.name.isEmpty()) {
            p.name = p.host();
        }
        p.yaml = YamlBuilder.from(p);
        LoftApp.of(this).profiles().put(p);
    }

    private void set(int id, String s) {
        ((EditText) findViewById(id)).setText(s == null ? "" : s);
    }

    private String text(int id) {
        return ((EditText) findViewById(id)).getText().toString().trim();
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
