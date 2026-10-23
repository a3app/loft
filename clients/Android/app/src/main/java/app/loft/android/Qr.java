package app.loft.android;

import android.graphics.Bitmap;

import com.google.zxing.BarcodeFormat;
import com.google.zxing.EncodeHintType;
import com.google.zxing.qrcode.QRCodeWriter;

import java.util.HashMap;
import java.util.Map;

/** QR bitmap for share and TV pairing. Dark mark on snow. */
final class Qr {
    static Bitmap encode(String s, int size) {
        if (s == null || s.isEmpty() || size < 32) {
            return null;
        }
        try {
            Map<EncodeHintType, Object> h = new HashMap<>();
            h.put(EncodeHintType.MARGIN, 1);
            com.google.zxing.common.BitMatrix m = new QRCodeWriter()
                    .encode(s, BarcodeFormat.QR_CODE, size, size, h);
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
