/*
 * Android client, phone build: the import screen. Reads a connection from a
 * camera QR code (CameraX + ZXing), the clipboard, a file or a gallery image.
 * A TV pairing QR (see PairUrl and the TV build's TvPair) sends the phone's
 * profiles to the TV instead. Opened from MainActivity and EditActivity.
 */
package app.loft.android;

import android.Manifest;
import android.content.ClipboardManager;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.net.Uri;
import android.app.AlertDialog;
import android.os.Bundle;
import android.widget.EditText;
import android.widget.Toast;

import androidx.annotation.NonNull;
import androidx.camera.core.CameraSelector;
import androidx.camera.core.ImageAnalysis;
import androidx.camera.core.ImageProxy;
import androidx.camera.core.Preview;
import androidx.camera.lifecycle.ProcessCameraProvider;
import androidx.camera.view.PreviewView;
import androidx.core.content.ContextCompat;

import com.google.common.util.concurrent.ListenableFuture;
import com.google.zxing.BarcodeFormat;
import com.google.zxing.BinaryBitmap;
import com.google.zxing.MultiFormatReader;
import com.google.zxing.RGBLuminanceSource;
import com.google.zxing.Result;
import com.google.zxing.common.HybridBinarizer;

import java.io.InputStream;
import java.nio.ByteBuffer;
import java.util.List;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Import screen: camera QR scanner plus paste, file and gallery image.
 * Whatever is read goes through accept(). A TV pairing QR sends all
 * profiles to the TV instead of importing anything. With the "return" extra
 * the raw text goes back to the caller (EditActivity) instead of being
 * saved; with the "file" extra the file picker opens at once.
 */
public final class ScanActivity extends LoftActivity {
    /** Request codes that tell apart the camera permission, file picker and gallery results. */
    private static final int REQ_CAM = 3;
    private static final int REQ_FILE = 4;
    private static final int REQ_GAL = 5;
    // Set once a code is accepted, so the camera's stream of frames does not
    // import the same code many times. Cleared when the import is cancelled.
    private final AtomicBoolean got = new AtomicBoolean(false);
    private final MultiFormatReader reader = new MultiFormatReader();

    /**
     * Wires close, paste, add, file and gallery buttons. Then either opens the file picker at once
     * ("file" extra) or starts the camera, asking for camera permission first if needed.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_scan);
        Fit.buttonRow(findViewById(R.id.scan_pick));
        Fit.buttonRow(findViewById(R.id.scan_actions));
        findViewById(R.id.btn_close).setOnClickListener(v -> finish());
        findViewById(R.id.btn_paste).setOnClickListener(v -> {
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm != null && cm.getPrimaryClip() != null && cm.getPrimaryClip().getItemCount() > 0) {
                CharSequence t = cm.getPrimaryClip().getItemAt(0).coerceToText(this);
                ((EditText) findViewById(R.id.paste)).setText(t);
            }
        });
        findViewById(R.id.btn_add).setOnClickListener(v ->
                accept(((EditText) findViewById(R.id.paste)).getText().toString()));
        findViewById(R.id.btn_file).setOnClickListener(v -> {
            Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
            i.addCategory(Intent.CATEGORY_OPENABLE);
            i.setType("*/*");
            startActivityForResult(i, REQ_FILE);
        });
        findViewById(R.id.btn_gal).setOnClickListener(v -> {
            Intent i = new Intent(Intent.ACTION_GET_CONTENT);
            i.setType("image/*");
            startActivityForResult(i, REQ_GAL);
        });
        if (getIntent().getBooleanExtra("file", false)) {
            findViewById(R.id.btn_file).performClick();
        } else if (checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
            startCam();
        } else {
            requestPermissions(new String[]{Manifest.permission.CAMERA}, REQ_CAM);
        }
    }

    /**
     * Starts the camera once permission is granted; otherwise says the camera is unavailable,
     * leaving paste, file and gallery usable.
     */
    @Override
    public void onRequestPermissionsResult(int requestCode, @NonNull String[] permissions, @NonNull int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == REQ_CAM && grantResults.length > 0 && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
            startCam();
        } else if (requestCode == REQ_CAM) {
            Toast.makeText(this, R.string.no_camera, Toast.LENGTH_SHORT).show();
        }
    }

    /**
     * Reads the chosen file as text, or decodes a QR code from the chosen gallery image, and
     * passes the result to accept(). Read errors are shown as a toast.
     */
    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (resultCode != RESULT_OK || data == null || data.getData() == null) {
            return;
        }
        Uri u = data.getData();
        if (requestCode == REQ_FILE) {
            try (InputStream in = getContentResolver().openInputStream(u)) {
                if (in != null) {
                    accept(MainActivity.readAll(in));
                }
            } catch (Exception e) {
                Toast.makeText(this, e.getMessage(), Toast.LENGTH_SHORT).show();
            }
            return;
        }
        if (requestCode == REQ_GAL) {
            try (InputStream in = getContentResolver().openInputStream(u)) {
                Bitmap bm = BitmapFactory.decodeStream(in);
                String t = decodeBitmap(bm);
                if (t != null) {
                    accept(t);
                }
            } catch (Exception e) {
                Toast.makeText(this, e.getMessage(), Toast.LENGTH_SHORT).show();
            }
        }
    }

    /**
     * Binds the back camera to this screen with a live preview and a frame analyser. Only the
     * newest frame is kept, so slow decoding never builds a queue.
     */
    private void startCam() {
        ListenableFuture<ProcessCameraProvider> f = ProcessCameraProvider.getInstance(this);
        f.addListener(() -> {
            try {
                ProcessCameraProvider provider = f.get();
                Preview preview = new Preview.Builder().build();
                PreviewView pv = findViewById(R.id.preview);
                preview.setSurfaceProvider(pv.getSurfaceProvider());
                ImageAnalysis an = new ImageAnalysis.Builder()
                        .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                        .build();
                an.setAnalyzer(Executors.newSingleThreadExecutor(), this::analyze);
                provider.unbindAll();
                provider.bindToLifecycle(this, CameraSelector.DEFAULT_BACK_CAMERA, preview, an);
            } catch (Exception e) {
                Journal.add("camera " + e.getMessage());
            }
        }, ContextCompat.getMainExecutor(this));
    }

    /**
     * Camera frame callback, run on a background thread. Skips work once a code was accepted,
     * always closes the frame so the camera can deliver the next one, and hands found text to
     * accept() on the UI thread.
     */
    private void analyze(ImageProxy image) {
        if (got.get()) {
            image.close();
            return;
        }
        try {
            String t = decodeY(image);
            if (t != null && !t.isEmpty()) {
                runOnUiThread(() -> accept(t));
            }
        } finally {
            image.close();
        }
    }

    /**
     * Decodes a QR code from the frame's luminance (Y) plane only, which is all
     * ZXing needs, turned into grey pixels. Row stride may be wider than the
     * image, so rows are copied one by one.
     */
    private String decodeY(ImageProxy image) {
        ImageProxy.PlaneProxy y = image.getPlanes()[0];
        ByteBuffer buf = y.getBuffer();
        byte[] data = new byte[buf.remaining()];
        buf.get(data);
        int w = image.getWidth();
        int h = image.getHeight();
        int[] pixels = new int[w * h];
        int row = y.getRowStride();
        for (int j = 0; j < h; j++) {
            for (int i = 0; i < w; i++) {
                int v = data[j * row + i] & 0xff;
                pixels[j * w + i] = 0xff000000 | (v << 16) | (v << 8) | v;
            }
        }
        return decodePixels(pixels, w, h);
    }

    /** Decodes a QR code from a still image (gallery), or returns null. */
    private String decodeBitmap(Bitmap bm) {
        if (bm == null) {
            return null;
        }
        int w = bm.getWidth();
        int h = bm.getHeight();
        int[] px = new int[w * h];
        bm.getPixels(px, 0, w, 0, 0, w, h);
        return decodePixels(px, w, h);
    }

    /**
     * Runs ZXing on ARGB pixels and returns the code text, or null when nothing was found.
     * The reader is reset after every attempt because it is reused for every frame.
     */
    private String decodePixels(int[] px, int w, int h) {
        try {
            BinaryBitmap bitmap = new BinaryBitmap(new HybridBinarizer(new RGBLuminanceSource(w, h, px)));
            Result r = reader.decode(bitmap);
            if (r != null && (r.getBarcodeFormat() == BarcodeFormat.QR_CODE || r.getText() != null)) {
                return r.getText();
            }
        } catch (Exception ignored) {
        } finally {
            reader.reset();
        }
        return null;
    }

    /**
     * Handles scanned or pasted text. A pairing URL from a TV gets all profiles
     * POSTed to it. Otherwise the text is parsed and, after the user confirms
     * the list, the profiles are added.
     */
    private void accept(String raw) {
        if (raw == null || raw.trim().isEmpty() || !got.compareAndSet(false, true)) {
            return;
        }
        // 1. A TV pairing QR: send every profile to the TV and close on success.
        if (PairUrl.isPair(raw)) {
            String body = LoftApp.of(this).profiles().shareAll();
            if (body.isEmpty()) {
                got.set(false);
                Toast.makeText(this, R.string.empty_conns, Toast.LENGTH_SHORT).show();
                return;
            }
            PairUrl.send(this, raw, body, (ok, err) -> {
                if (ok) {
                    Toast.makeText(this, R.string.sent_tv, Toast.LENGTH_SHORT).show();
                    finish();
                } else {
                    got.set(false);
                    Toast.makeText(this, err == null ? getString(R.string.paste_hint) : err,
                            Toast.LENGTH_SHORT).show();
                }
            });
            return;
        }
        // 2. Otherwise parse links or YAML into profiles; nothing usable means try again.
        List<Profile> list = Importer.parse(raw);
        if (list.isEmpty()) {
            got.set(false);
            Toast.makeText(this, R.string.paste_hint, Toast.LENGTH_SHORT).show();
            return;
        }
        // 3. EditActivity only wants the raw text back, without saving anything here.
        if (getIntent().getBooleanExtra("return", false)) {
            setResult(RESULT_OK, new Intent().putExtra("raw", raw));
            finish();
            return;
        }
        // 4. Show what will be added (one line per profile) and add only after the user confirms.
        Profile first = list.get(0);
        String title = first.name.isEmpty() ? first.host() : first.name;
        StringBuilder msg = new StringBuilder();
        for (Profile p : list) {
            if (msg.length() > 0) {
                msg.append('\n');
            }
            msg.append(p.side());
            if (!p.host().isEmpty()) {
                msg.append("  ").append(p.host());
            }
        }
        AlertDialog dlg = new AlertDialog.Builder(this)
                .setTitle(title.isEmpty() ? getString(R.string.imported) : title)
                .setMessage(msg.toString())
                .setPositiveButton(R.string.confirm_imported, (d, w) -> {
                    LoftApp.of(this).profiles().addAll(list);
                    Toast.makeText(this, R.string.imported, Toast.LENGTH_SHORT).show();
                    finish();
                })
                .setNegativeButton(R.string.cancel, (d, w) -> got.set(false))
                .setOnCancelListener(d -> got.set(false))
                .create();
        dlg.show();
        Fit.alertButtons(dlg);
    }
}
