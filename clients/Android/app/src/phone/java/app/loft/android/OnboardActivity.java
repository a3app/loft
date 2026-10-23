package app.loft.android;

import android.app.Activity;
import android.content.ClipboardManager;
import android.content.Intent;
import android.net.VpnService;
import android.os.Bundle;
import android.view.View;
import android.widget.EditText;
import android.widget.TextView;
import android.widget.Toast;

import java.util.List;

public final class OnboardActivity extends LoftActivity {
    private static final String[] METHODS = {"auto", "get", "ws", "h2", "quic", "h3", "l3"};
    private static final int REQ_VPN = 1;
    private int step;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_onboard);
        findViewById(R.id.btn_scan).setOnClickListener(v ->
                startActivity(new Intent(this, ScanActivity.class)));
        findViewById(R.id.btn_paste).setOnClickListener(v -> paste());
        findViewById(R.id.btn_hand).setOnClickListener(v -> go(1));
        findViewById(R.id.btn_cont).setOnClickListener(v -> {
            if (text(R.id.token).isEmpty()) {
                return;
            }
            go(2);
        });
        findViewById(R.id.btn_back1).setOnClickListener(v -> go(0));
        findViewById(R.id.btn_back2).setOnClickListener(v -> go(1));
        findViewById(R.id.btn_adv).setOnClickListener(v -> {
            View adv = findViewById(R.id.adv);
            adv.setVisibility(adv.getVisibility() == View.VISIBLE ? View.GONE : View.VISIBLE);
        });
        findViewById(R.id.btn_go).setOnClickListener(v -> connectHand());
        Seg.bind(findViewById(R.id.method_seg), 0, null);
        go(0);
    }

    @Override
    protected void onResume() {
        super.onResume();
        if (!LoftApp.of(this).profiles().all().isEmpty()) {
            Prefs.p(this).edit().putBoolean("onboard_done", true).apply();
            finish();
        }
    }

    private void go(int s) {
        step = s;
        findViewById(R.id.step0).setVisibility(s == 0 ? View.VISIBLE : View.GONE);
        findViewById(R.id.step1).setVisibility(s == 1 ? View.VISIBLE : View.GONE);
        findViewById(R.id.step2).setVisibility(s == 2 ? View.VISIBLE : View.GONE);
        TextView st = findViewById(R.id.step);
        TextView title = findViewById(R.id.title);
        TextView body = findViewById(R.id.body);
        if (s == 0) {
            st.setText("");
            title.setText(R.string.app_name);
            body.setText(R.string.scan_or_paste);
        } else if (s == 1) {
            st.setText(R.string.step_1);
            title.setText(R.string.key_title);
            body.setText(R.string.key_body);
        } else {
            st.setText(R.string.step_2);
            title.setText(R.string.host);
            body.setText(R.string.url_hint);
        }
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
        List<Profile> got = Importer.parse(t.toString());
        if (got.isEmpty()) {
            Toast.makeText(this, R.string.paste_hint, Toast.LENGTH_SHORT).show();
            return;
        }
        LoftApp.of(this).profiles().addAll(got);
        Prefs.p(this).edit().putBoolean("onboard_done", true).apply();
        askVpn();
    }

    private void connectHand() {
        Profile p = new Profile();
        p.token = text(R.id.token);
        String url = text(R.id.url);
        if (url.isEmpty()) {
            return;
        }
        p.url = url;
        int mi = Seg.selected(findViewById(R.id.method_seg));
        String type = METHODS[mi];
        if ("auto".equals(type)) {
            type = "get";
        }
        p.kind = type;
        p.path = text(R.id.path);
        if (p.name.isEmpty()) {
            p.name = p.host();
        }
        p.yaml = YamlBuilder.from(p);
        LoftApp.of(this).profiles().put(p);
        LoftApp.of(this).profiles().setActive(p.id);
        Prefs.p(this).edit().putBoolean("onboard_done", true).apply();
        askVpn();
    }

    private void askVpn() {
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            startActivityForResult(prep, REQ_VPN);
            return;
        }
        LoftVpnService.requestStart(this);
        finish();
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_VPN && resultCode == Activity.RESULT_OK) {
            LoftVpnService.requestStart(this);
        }
        finish();
    }

    private String text(int id) {
        return ((EditText) findViewById(id)).getText().toString().trim();
    }
}
