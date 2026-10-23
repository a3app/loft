package app.loft.android;

import android.os.Bundle;
import android.widget.TextView;

public final class AboutActivity extends LoftActivity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_about);
        findViewById(R.id.btn_back).setOnClickListener(v -> finish());
        String ver = "1.0.26";
        try {
            ver = getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (Exception ignored) {
        }
        ((TextView) findViewById(R.id.ver)).setText(ver);
        String rss = Rss.vm();
        TextView r = findViewById(R.id.rss);
        if (rss.isEmpty()) {
            r.setVisibility(android.view.View.GONE);
        } else {
            r.setText("RSS " + rss);
        }
    }
}
