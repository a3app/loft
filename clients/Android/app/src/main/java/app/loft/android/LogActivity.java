/*
 * Android client, shared code: the log screen used by both phone and TV
 * builds. It shows the text kept by Journal and lets the user copy, share
 * or clear it.
 */
package app.loft.android;

import android.app.AlertDialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.os.Bundle;
import android.widget.TextView;

/**
 * Shows the session journal (Journal) with copy, share and clear. Redraws on
 * every Bus event while visible. Journal keeps at most 400 lines in memory;
 * each redraw replaces the whole text. The journal text is already redacted
 * by Journal before it is stored, so copy and share pass it on as is.
 */
public final class LogActivity extends LoftActivity implements Bus.Listener {
    /** The text view holding the whole journal. */
    private TextView body;

    /** Inflates the screen and wires back, copy, share and clear (clear asks first). */
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

    /** Starts listening for new log lines and shows the current text. */
    @Override
    protected void onResume() {
        super.onResume();
        Bus.watch(this);
        paint();
    }

    /** Stops listening while the screen is hidden. */
    @Override
    protected void onPause() {
        Bus.drop(this);
        super.onPause();
    }

    /** Bus event: the journal (or other state) changed, so redraw. */
    @Override
    public void onLoft() {
        paint();
    }

    /** Replaces the text with the whole journal. */
    private void paint() {
        body.setText(Journal.text());
    }
}
