/*
 * Android client, shared code: the About screen (version, protocol table,
 * "made with" line with HeartView). On the github build Channel.attach also
 * adds the update button and status here; on the store build it does nothing.
 */
package app.loft.android;

import android.content.res.Configuration;
import android.os.Bundle;
import android.util.DisplayMetrics;
import android.util.TypedValue;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.TextView;

/** About screen: app version and a short table of the supported protocols. */
public final class AboutActivity extends LoftActivity {
    @Override
    /** Fills the version and the protocol rows, scales the layout, then lets the update channel attach. */
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_about);
        findViewById(R.id.btn_back).setOnClickListener(v -> finish());
        String ver = "";
        try {
            ver = getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (Exception ignored) {
        }
        ((TextView) findViewById(R.id.ver)).setText(ver == null ? "" : ver);
        bindProto(R.id.proto_loft, R.string.about_loft_k, R.string.about_fronts);
        bindProto(R.id.proto_sstp, R.string.about_sstp_k, R.string.about_sstp_v);
        bindProto(R.id.proto_trojan, R.string.about_trojan_k, R.string.about_trojan_v);
        bindProto(R.id.proto_vless, R.string.about_vless_k, R.string.about_vless_v);
        scaleToScreen();
        Channel.attach(this);
    }

    /** Sets the name ({@code k}) and description ({@code v}) strings of one protocol row. */
    private void bindProto(int row, int k, int v) {
        View r = findViewById(row);
        ((TextView) r.findViewById(R.id.k)).setText(k);
        ((TextView) r.findViewById(R.id.v)).setText(v);
    }

    /** Lockup, type and proto table follow the window; spacers in XML pin to the edges. */
    private void scaleToScreen() {
        if (getResources().getConfiguration().orientation == Configuration.ORIENTATION_LANDSCAPE) {
            return;
        }
        // Scale factor relative to a 360x700 dp phone, kept between 1 and 1.45.
        DisplayMetrics dm = getResources().getDisplayMetrics();
        float d = dm.density;
        float sw = dm.widthPixels / d;
        float sh = dm.heightPixels / d;
        float s = Math.min(sw / 360f, sh / 700f);
        if (s < 1f) {
            s = 1f;
        }
        if (s > 1.45f) {
            s = 1.45f;
        }
        int side = Math.round(Math.max(24f, sw * 0.09f) * d);
        findViewById(R.id.about_body).setPadding(side, 0, side, Math.round(8 * d));

        // Logo mark, then text sizes, then the protocol table, then the heart.
        int mark = Math.round(96f * s * d);
        View m = findViewById(R.id.mark);
        ViewGroup.LayoutParams mp = m.getLayoutParams();
        mp.width = mark;
        mp.height = mark;
        m.setLayoutParams(mp);

        setSp(R.id.title, 26f * s);
        setSp(R.id.ver, 14f * s);
        setSp(R.id.kick, 12.5f * s);
        setSp(R.id.blurb, 16f * s);
        setSp(R.id.made_pre, 13.5f * s);
        setSp(R.id.made_post, 13.5f * s);

        int kW = Math.round(84f * s * d);
        int[] rows = {R.id.proto_loft, R.id.proto_sstp, R.id.proto_trojan, R.id.proto_vless};
        for (int id : rows) {
            View row = findViewById(id);
            TextView k = row.findViewById(R.id.k);
            TextView v = row.findViewById(R.id.v);
            LinearLayout.LayoutParams kp = (LinearLayout.LayoutParams) k.getLayoutParams();
            kp.width = kW;
            k.setLayoutParams(kp);
            k.setTextSize(TypedValue.COMPLEX_UNIT_SP, 14.5f * s);
            v.setTextSize(TypedValue.COMPLEX_UNIT_SP, 12.5f * s);
        }

        View heart = findViewById(R.id.heart);
        ViewGroup.LayoutParams hp = heart.getLayoutParams();
        hp.width = Math.round(16f * s * d);
        hp.height = Math.round(14f * s * d);
        heart.setLayoutParams(hp);
    }

    /** Sets the text size of a TextView in sp. */
    private void setSp(int id, float sp) {
        ((TextView) findViewById(id)).setTextSize(TypedValue.COMPLEX_UNIT_SP, sp);
    }
}
