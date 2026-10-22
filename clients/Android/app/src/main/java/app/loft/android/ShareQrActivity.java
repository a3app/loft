package app.loft.android;

import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.graphics.Bitmap;
import android.os.Bundle;
import android.widget.Button;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

import androidx.appcompat.app.AppCompatActivity;

import com.google.zxing.BarcodeFormat;
import com.google.zxing.EncodeHintType;
import com.google.zxing.qrcode.QRCodeWriter;

import java.util.HashMap;
import java.util.Map;

public final class ShareQrActivity extends AppCompatActivity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        Profile p = LoftApp.of(this).profiles().get(getIntent().getStringExtra("id"));
        String uri = p == null ? "" : YamlBuilder.emit(p);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF1A1714);
        int pad = (int) (20 * getResources().getDisplayMetrics().density);
        root.setPadding(pad, pad, pad, pad);
        ImageView qr = new ImageView(this);
        qr.setAdjustViewBounds(true);
        if (!uri.isEmpty()) {
            qr.setImageBitmap(encode(uri));
        }
        TextView line = new TextView(this);
        line.setText(uri);
        line.setTextIsSelectable(true);
        line.setTextColor(0xFFB7A99A);
        line.setTypeface(android.graphics.Typeface.MONOSPACE);
        line.setPadding(0, pad, 0, pad);
        TextView warn = new TextView(this);
        warn.setText(R.string.share_warn);
        warn.setTextColor(0xFFE8B84A);
        Button copy = new Button(this);
        copy.setText(R.string.copy);
        copy.setOnClickListener(v -> {
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm != null) {
                cm.setPrimaryClip(ClipData.newPlainText("loft", uri));
            }
        });
        Button share = new Button(this);
        share.setText(R.string.share);
        share.setOnClickListener(v -> {
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, uri);
            startActivity(Intent.createChooser(i, getString(R.string.share)));
        });
        root.addView(qr);
        root.addView(line);
        root.addView(warn);
        root.addView(copy);
        root.addView(share);
        setContentView(root);
    }

    private Bitmap encode(String s) {
        try {
            Map<EncodeHintType, Object> h = new HashMap<>();
            h.put(EncodeHintType.MARGIN, 1);
            com.google.zxing.common.BitMatrix m = new QRCodeWriter().encode(s, BarcodeFormat.QR_CODE, 512, 512, h);
            int w = m.getWidth();
            int[] px = new int[w * w];
            for (int y = 0; y < w; y++) {
                for (int x = 0; x < w; x++) {
                    px[y * w + x] = m.get(x, y) ? 0xFF1A1714 : 0xFFF3EDE4;
                }
            }
            return Bitmap.createBitmap(px, w, w, Bitmap.Config.ARGB_8888);
        } catch (Exception e) {
            return null;
        }
    }
}
