/*
 * Android client, shared code: draws QR codes with the ZXing library. The
 * phone uses it to share a connection as a QR code; the TV build uses it to
 * show a pairing address that the phone scans (see PairUrl, TvPair).
 */
package app.loft.android;

import android.graphics.Bitmap;

import com.google.zxing.BarcodeFormat;
import com.google.zxing.EncodeHintType;
import com.google.zxing.qrcode.QRCodeWriter;

import java.util.HashMap;
import java.util.Map;

/**
 * QR bitmap for share and TV pairing. Dark mark on snow (the app's ink
 * 0xFF1A1714 on paper 0xFFF3EDE4, rather than pure black on white).
 */
final class Qr {
    /**
     * Encodes {@code s} into a square QR bitmap about {@code size} pixels wide,
     * in the app's dark-ink-on-light colours with a 1-module quiet border.
     * Returns null for empty text, a size under 32 px, or an encoding error
     * (for example text too long for a QR code). The work is CPU-bound:
     * encoding plus one pixel per loop step, about a quarter of a million
     * for 512 px, done on the caller's thread.
     */
    static Bitmap encode(String s, int size) {
        if (s == null || s.isEmpty() || size < 32) {
            return null;
        }
        try {
            Map<EncodeHintType, Object> h = new HashMap<>();
            h.put(EncodeHintType.MARGIN, 1);
            com.google.zxing.common.BitMatrix m = new QRCodeWriter()
                    .encode(s, BarcodeFormat.QR_CODE, size, size, h);
            // ZXing returns a matrix at least size × size, scaled up to whole
            // modules; copy it into an ARGB pixel array and build the bitmap.
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
