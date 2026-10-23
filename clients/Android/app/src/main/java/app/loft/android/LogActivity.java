package app.loft.android;

import android.app.AlertDialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.os.Bundle;
import android.widget.TextView;

public final class LogActivity extends LoftActivity implements Bus.Listener {
    private TextView body;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_log);
        body = findViewById(R.id.body);
        findViewById(R.id.btn_back).setOnClickListener(v -> finish());
        findViewById(R.id.btn_copy).setOnClickListener(v -> {
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm != null) {
                cm.setPrimaryClip(ClipData.newPlainText("loft", Journal.text()));
            }
        });
        findViewById(R.id.btn_share).setOnClickListener(v -> {
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, Journal.text());
            startActivity(Intent.createChooser(i, getString(R.string.share)));
        });
        findViewById(R.id.btn_clear).setOnClickListener(v ->
                new AlertDialog.Builder(this)
                        .setMessage(R.string.confirm_clear)
                        .setPositiveButton(R.string.clear_log, (d, w) -> Journal.clear())
                        .setNegativeButton(R.string.cancel, null)
                        .show());
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
}
