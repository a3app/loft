/*
 * Android client, phone build: the "share profile" screen. Turns a saved
 * profile back into a loft:// link (YamlBuilder.emit) and shows it as text
 * and as a QR code (Qr), so another device can import it with ScanActivity.
 */
package app.loft.android;

import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.os.Bundle;
import android.widget.ImageView;
import android.widget.TextView;

/**
 * Shows one profile as a share link and its QR code, with copy and share.
 * The link contains the member key, so anyone who scans it can connect.
 * Started with the profile id in the "id" extra.
 */
public final class ShareQrActivity extends LoftActivity {
    /**
     * Builds the link for the profile named by the "id" extra and wires close, copy and share.
     * An unknown id shows an empty screen instead of crashing.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_share);
        Profile p = LoftApp.of(this).profiles().get(getIntent().getStringExtra("id"));
        String uri = p == null ? "" : YamlBuilder.emit(p);
        findViewById(R.id.btn_close).setOnClickListener(v -> finish());
        ImageView qr = findViewById(R.id.qr);
        if (!uri.isEmpty()) {
            qr.setImageBitmap(Qr.encode(uri, 512));
        }
        ((TextView) findViewById(R.id.line)).setText(uri);
        findViewById(R.id.btn_copy).setOnClickListener(v -> {
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm != null) {
                cm.setPrimaryClip(ClipData.newPlainText("loft", uri));
            }
        });
        findViewById(R.id.btn_share).setOnClickListener(v -> {
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, uri);
            startActivity(Intent.createChooser(i, getString(R.string.share)));
        });
    }
}
