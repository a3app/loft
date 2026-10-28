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

    /**
     * One scale drives mark, type, proto column and the heart. Sizes are
     * dp, not sp: a large system font grows the lockup instead of blowing
     * the type past the mark. Spacers in XML still pin the blocks to the
     * edges; the screen scrolls if the scaled copy no longer fits.
     */
    private void scaleToScreen() {
        DisplayMetrics dm = getResources().getDisplayMetrics();
        float d = dm.density;
        float sw = dm.widthPixels / d;
        float sh = dm.heightPixels / d;
        boolean land = getResources().getConfiguration().orientation
                == Configuration.ORIENTATION_LANDSCAPE;
        float screen = land
                ? Math.min(sw / 640f, sh / 360f)
                : Math.min(sw / 360f, sh / 700f);
        if (screen < 1f) {
            screen = 1f;
        }
        if (screen > 1.45f) {
            screen = 1.45f;
        }
        float font = getResources().getConfiguration().fontScale;
        if (font < 0.85f) {
            font = 0.85f;
        }
        if (font > 1.3f) {
            font = 1.3f;
        }
        float s = screen * font;
        if (s > 1.55f) {
            s = 1.55f;
        }

        // Padding follows the window only, so larger type keeps column width.
        int side = Math.round(Math.max(24f, Math.min(sw * 0.06f, 28f)) * d);
        findViewById(R.id.about_body).setPadding(side, 0, side, Math.round(8 * d));

        int mark = Math.round((land ? 52f : 96f) * s * d);
        View m = findViewById(R.id.mark);
        ViewGroup.LayoutParams mp = m.getLayoutParams();
        mp.width = mark;
        mp.height = mark;
        m.setLayoutParams(mp);

        setDp(R.id.title, (land ? 22f : 26f) * s);
        setDp(R.id.ver, 14f * s);
        setDp(R.id.kick, 12.5f * s);
        setDp(R.id.blurb, 16f * s);
        setDp(R.id.made_pre, 13.5f * s);
        setDp(R.id.made_post, 13.5f * s);

        TextView chBtn = findViewById(R.id.btn_channel);
        if (chBtn != null) {
            chBtn.setTextSize(TypedValue.COMPLEX_UNIT_DIP, 13f * s);
            chBtn.setMinHeight(Math.round(28f * s * d));
        }
        TextView chSt = findViewById(R.id.channel_status);
        if (chSt != null) {
            chSt.setTextSize(TypedValue.COMPLEX_UNIT_DIP, 11f * s);
        }

        if (land) {
            int indent = mark + Math.round(10f * s * d);
            setStartMargin(findViewById(R.id.ver), indent);
            if (chBtn != null && chBtn.getParent() instanceof View) {
                setStartMargin((View) chBtn.getParent(), indent);
            }
        }

        int[] rows = {R.id.proto_loft, R.id.proto_sstp, R.id.proto_trojan, R.id.proto_vless};
        int kW = Math.round(56f * s * d);
        for (int id : rows) {
            View row = findViewById(id);
            TextView k = row.findViewById(R.id.k);
            TextView v = row.findViewById(R.id.v);
            k.setTextSize(TypedValue.COMPLEX_UNIT_DIP, 14.5f * s);
            v.setTextSize(TypedValue.COMPLEX_UNIT_DIP, 12.5f * s);
            LinearLayout.LayoutParams rp = (LinearLayout.LayoutParams) row.getLayoutParams();
            rp.topMargin = Math.round(3f * s * d);
            row.setLayoutParams(rp);
            LinearLayout.LayoutParams kp = (LinearLayout.LayoutParams) k.getLayoutParams();
            kp.width = ViewGroup.LayoutParams.WRAP_CONTENT;
            k.setLayoutParams(kp);
            k.measure(
                    View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED),
                    View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
            kW = Math.max(kW, k.getMeasuredWidth());
        }
        for (int id : rows) {
            TextView k = findViewById(id).findViewById(R.id.k);
            LinearLayout.LayoutParams kp = (LinearLayout.LayoutParams) k.getLayoutParams();
            kp.width = kW;
            k.setLayoutParams(kp);
        }

        View heart = findViewById(R.id.heart);
        ViewGroup.LayoutParams hp = heart.getLayoutParams();
        hp.width = Math.round(16f * s * d);
        hp.height = Math.round(14f * s * d);
        heart.setLayoutParams(hp);
    }

    /** Sets the text size of a TextView in dp, so fontScale does not apply twice. */
    private void setDp(int id, float dp) {
        ((TextView) findViewById(id)).setTextSize(TypedValue.COMPLEX_UNIT_DIP, dp);
    }

    /** Start margin in px; used to keep landscape version/channel under the title. */
    private static void setStartMargin(View v, int px) {
        if (v == null) {
            return;
        }
        ViewGroup.MarginLayoutParams lp = (ViewGroup.MarginLayoutParams) v.getLayoutParams();
        lp.setMarginStart(px);
        v.setLayoutParams(lp);
    }
}
