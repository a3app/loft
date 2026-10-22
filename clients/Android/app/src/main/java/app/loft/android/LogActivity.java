package app.loft.android;

import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.os.Bundle;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import androidx.appcompat.app.AppCompatActivity;

public final class LogActivity extends AppCompatActivity implements Bus.Listener {
    private TextView body;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF1A1714);
        LinearLayout bar = new LinearLayout(this);
        Button copy = btn(R.string.copy);
        Button share = btn(R.string.share);
        Button clear = btn(R.string.clear_log);
        bar.addView(copy);
        bar.addView(share);
        bar.addView(clear);
        body = new TextView(this);
        body.setTextColor(0xFFB7A99A);
        body.setTextIsSelectable(true);
        body.setTypeface(android.graphics.Typeface.MONOSPACE);
        body.setPadding(24, 24, 24, 24);
        ScrollView sc = new ScrollView(this);
        sc.addView(body);
        root.addView(bar);
        root.addView(sc, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1));
        setContentView(root);
        copy.setOnClickListener(v -> {
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm != null) {
                cm.setPrimaryClip(ClipData.newPlainText("loft", Journal.text()));
            }
        });
        share.setOnClickListener(v -> {
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, Journal.text());
            startActivity(Intent.createChooser(i, getString(R.string.share)));
        });
        clear.setOnClickListener(v -> Journal.clear());
        paint();
    }

    @Override
    protected void onResume() {
        super.onResume();
        Bus.watch(this);
        paint();
    }

    @Override
    protected void onPause() {
        Bus.drop(this);
        super.onPause();
    }

    @Override
    public void onLoft() {
        paint();
    }

    private void paint() {
        body.setText(Journal.text());
    }

    private Button btn(int id) {
        Button b = new Button(this);
        b.setText(id);
        return b;
    }
}
