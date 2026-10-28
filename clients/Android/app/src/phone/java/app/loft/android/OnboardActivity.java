/*
 * Android client, phone build: the first-run wizard. MainActivity opens it
 * while no profile exists. A profile can come from a scanned QR code
 * (ScanActivity), a pasted link (Importer) or typed fields (built into YAML
 * with YamlBuilder); then the VPN is started.
 */
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

/**
 * First-run screen, shown while there are no profiles. Step 0 offers scan,
 * paste or manual entry; step 1 asks for the member key; step 2 for the
 * host, front and optional path and edge. After a profile is added it asks
 * for VPN permission and starts the service.
 */
public final class OnboardActivity extends LoftActivity {
    /**
     * Front choices in the segmented control, in its order (a front is the way the tunnel
     * is carried over the web: plain HTTPS GET, WebSocket, HTTP/2, QUIC, HTTP/3 or L3).
     * "auto" is stored as GET. Same list as in EditActivity.
     */
    private static final String[] METHODS = {"auto", "get", "ws", "h2", "quic", "h3", "l3"};
    /** Request code for the system VPN consent dialog. */
    private static final int REQ_VPN = 1;
    /** Step currently shown (0, 1 or 2). Written by go(); nothing reads it back at the moment. */
    private int step;

    /**
     * Wires the buttons of all three steps and starts on step 0. Step 1's Continue does
     * nothing until a key is typed. The front control starts on "auto".
     */
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

    /**
     * Returning from ScanActivity with an imported profile ends onboarding. ScanActivity
     * saves profiles itself and returns no result, so the only way to notice an import is
     * to check the profile list whenever this screen comes back to the front.
     */
    @Override
    protected void onResume() {
        super.onResume();
        if (!LoftApp.of(this).profiles().all().isEmpty()) {
            Prefs.p(this).edit().putBoolean("onboard_done", true).apply();
            finish();
        }
    }

    /** Shows step {@code s} (0, 1 or 2) and sets its step label, title and help text. */
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

    /**
     * Imports from the clipboard; with an empty clipboard opens the scanner instead.
     * A successful import marks onboarding done and goes straight to VPN consent.
     * Unlike ScanActivity, no confirmation list is shown here.
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
        List<Profile> got = Importer.parse(t.toString());
        if (got.isEmpty()) {
            Toast.makeText(this, R.string.paste_hint, Toast.LENGTH_SHORT).show();
            return;
        }
        LoftApp.of(this).profiles().addAll(got);
        Prefs.p(this).edit().putBoolean("onboard_done", true).apply();
        askVpn();
    }

    /**
     * Builds a profile from the typed key, host, front, path and edge, saves it as the active
     * profile and moves on to VPN consent. Does nothing while the host is empty. The
     * profile keeps Profile's defaults for everything not asked here (the wizard only
     * creates loft profiles), and is named after its host.
     */
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
        p.edge = text(R.id.edge);
        if (p.name.isEmpty()) {
            p.name = p.host();
        }
        p.yaml = YamlBuilder.from(p);
        LoftApp.of(this).profiles().put(p);
        LoftApp.of(this).profiles().setActive(p.id);
        Prefs.p(this).edit().putBoolean("onboard_done", true).apply();
        askVpn();
    }

    /**
     * Shows the system VPN consent dialog when needed, then starts the service and closes
     * the wizard. VpnService.prepare() returns null when this app is already the approved
     * VPN app, so the service can start right away.
     */
    private void askVpn() {
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            startActivityForResult(prep, REQ_VPN);
            return;
        }
        LoftVpnService.requestStart(this);
        finish();
    }

    /**
     * Result of the VPN consent dialog: start the tunnel if allowed, then leave onboarding either way.
     */
    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_VPN && resultCode == Activity.RESULT_OK) {
            LoftVpnService.requestStart(this);
        }
        finish();
    }

    /** Trimmed text of an EditText by view id. */
    private String text(int id) {
        return ((EditText) findViewById(id)).getText().toString().trim();
    }
}
