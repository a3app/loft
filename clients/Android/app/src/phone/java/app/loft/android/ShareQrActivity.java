/*
 * Android client, phone build: the "share profile" screen. Turns a saved
 * profile back into a share link (YamlBuilder.emit: loft:// or sstp:// built
 * from its fields, or the original vless:// / trojan:// link) and shows it as
 * text and as a QR code (Qr), so another device can import it with
 * ScanActivity. Opened from MainActivity and EditActivity.
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
 * The link contains the member key (the secret that lets a device join the
 * node), so anyone who scans it can connect. Started with the profile id in
 * the "id" extra.
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
        // 1. Look the profile up and build its link; "" when the id is unknown.
        Profile p = LoftApp.of(this).profiles().get(getIntent().getStringExtra("id"));
        String uri = p == null ? "" : YamlBuilder.emit(p);
        findViewById(R.id.btn_close).setOnClickListener(v -> finish());
        // 2. Draw the QR code at 512 px. Encoding and filling about a quarter of a million
        //    pixels is too slow for the main thread on slower phones (the screen froze while
        //    opening), so it runs in the background (Ui.bg) and the picture is set when ready.
        //    Until then the image stays empty, as it already does when Qr.encode returns null
        //    (link too long for a QR code). Ui.bg drops the result if the screen was closed.
        ImageView qr = findViewById(R.id.qr);
        if (!uri.isEmpty()) {
            Ui.bg(this, () -> Qr.encode(uri, 512), (bm, err) -> {
                if (bm != null) {
                    qr.setImageBitmap(bm);
                }
            });
        }
        ((TextView) findViewById(R.id.line)).setText(uri);
        // 3. Copy puts the link on the clipboard; Share hands it to the system share sheet.
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
