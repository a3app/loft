package app.loft.android;

import android.os.Bundle;
import android.view.View;
import android.widget.TextView;

public final class AboutActivity extends LoftActivity {
    @Override
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
    }

    private void bindProto(int row, int k, int v) {
        View r = findViewById(row);
        ((TextView) r.findViewById(R.id.k)).setText(k);
        ((TextView) r.findViewById(R.id.v)).setText(v);
    }
}
