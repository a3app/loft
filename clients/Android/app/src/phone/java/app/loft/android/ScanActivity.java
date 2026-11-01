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
import java.util.concurrent.ExecutorService;
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
    /**
     * Set once a code is accepted, so the camera's stream of frames does not
     * import the same code many times. Cleared when the import is cancelled or
     * fails, so scanning can go on. Atomic because the camera thread reads it
     * while the main thread sets it.
     */
    private final AtomicBoolean got = new AtomicBoolean(false);
    /**
     * True while a code found by the camera is waiting to reach accept() on the
     * main thread. The analyser keeps getting frames in the meantime and would
     * find the same code again in each one; without this flag every frame would
     * queue another accept() call (and possibly another toast) on the main thread.
     */
    private final AtomicBoolean pending = new AtomicBoolean(false);
    /**
     * ZXing decoder for camera frames, reused for every frame to avoid
     * re-creating it. ZXing readers are not thread-safe, so this one is used
     * only on the camera analyser thread; gallery images get their own reader
     * (see readGallery).
     */
    private final MultiFormatReader camReader = new MultiFormatReader();
    /**
     * The one background thread that analyses camera frames. Created once (the
     * first startCam) and shut down in onDestroy; creating a new thread on every
     * startCam used to leak a thread each time.
     */
    private ExecutorService camExec;
    /**
     * Buffers for camera frames, reused while the frame size stays the same so
     * that a scan does not allocate (and the garbage collector does not have to
     * free) a couple of megabytes per frame. Touched only on the analyser thread.
     */
    private byte[] yBytes;
    private int[] yPixels;

    /**
     * Wires close, paste, add, file and gallery buttons. Then either opens the file picker at once
     * ("file" extra) or starts the camera, asking for camera permission first if needed.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_scan);
        // Let the button rows wrap at a large system font size instead of clipping.
        Fit.buttonRow(findViewById(R.id.scan_pick));
        Fit.buttonRow(findViewById(R.id.scan_actions));
        findViewById(R.id.btn_close).setOnClickListener(v -> finish());
        // Paste only copies the clipboard into the text box; Add then imports what the box
        // holds, so the user can check or edit the text first.
        findViewById(R.id.btn_paste).setOnClickListener(v -> {
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm != null && cm.getPrimaryClip() != null && cm.getPrimaryClip().getItemCount() > 0) {
                CharSequence t = cm.getPrimaryClip().getItemAt(0).coerceToText(this);
                ((EditText) findViewById(R.id.paste)).setText(t);
            }
        });
        findViewById(R.id.btn_add).setOnClickListener(v ->
                accept(((EditText) findViewById(R.id.paste)).getText().toString()));
        // File: the system document picker, any type ("*/*") so a .loft file can be picked
        // whatever type its provider reports. Gallery: any image app, to read a QR from a picture.
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
        // Opened from the add sheet's "paste" item: pre-fill the box from the clipboard and
        // focus it, but still wait for the user to tap Add.
        if (getIntent().getBooleanExtra("paste", false)) {
            EditText box = findViewById(R.id.paste);
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm != null && cm.getPrimaryClip() != null && cm.getPrimaryClip().getItemCount() > 0) {
                CharSequence t = cm.getPrimaryClip().getItemAt(0).coerceToText(this);
                if (t != null && t.length() > 0) {
                    box.setText(t);
                }
            }
            box.requestFocus();
        }
        // Opened for "file": jump straight to the picker and do not start the camera.
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
     * passes the result to accept(). Read errors are shown as a toast. A cancelled picker
     * (no result or no URI) does nothing. An image without a readable code is silently ignored.
     * Reading and decoding run in the background (Ui.bg): a provider may be slow (cloud
     * storage) and a photo decode is heavy, and either would freeze the screen on the main
     * thread. The result comes back on the main thread, or is dropped if the screen closed.
     */
    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (resultCode != RESULT_OK || data == null || data.getData() == null) {
            return;
        }
        Uri u = data.getData();
        if (requestCode == REQ_FILE) {
            Ui.bg(this, () -> {
                try (InputStream in = getContentResolver().openInputStream(u)) {
                    // null (provider returned no stream) means "nothing to import", as before.
                    return in == null ? null : MainActivity.readAll(in);
                }
            }, this::afterRead);
            return;
        }
        if (requestCode == REQ_GAL) {
            Ui.bg(this, () -> readGallery(u), this::afterRead);
        }
    }

    /**
     * Main-thread end of a file or gallery read: a read error becomes a toast, found text
     * goes to accept(), and null (no stream, or no code in the image) does nothing.
     */
    private void afterRead(String text, Exception err) {
        if (err != null) {
            Toast.makeText(this, err.getMessage(), Toast.LENGTH_SHORT).show();
        } else if (text != null) {
            accept(text);
        }
    }

    /** Longest side, in pixels, a gallery image is shrunk to before looking for a code. */
    private static final int GAL_MAX = 1600;

    /**
     * Background part of the gallery import: decodes the picture at a reduced size and looks
     * for a code in it. Returns the code text, or null when there is none.
     *
     * <p>A phone photo can be 50 megapixels; decoding it at full size takes hundreds of
     * megabytes and seconds of CPU, and ZXing then has to scan every pixel. A QR code that
     * fills a fair part of the picture is still easy to read at about 1600 px, so the image
     * is read twice: first only its size (inJustDecodeBounds, no pixels), then the pixels
     * with inSampleSize, which makes the decoder skip pixels as it reads (2 = half width and
     * half height, 4 = a quarter, ...; the decoder only uses powers of two).
     */
    private String readGallery(Uri u) throws Exception {
        // 1. Size only.
        BitmapFactory.Options o = new BitmapFactory.Options();
        o.inJustDecodeBounds = true;
        try (InputStream in = getContentResolver().openInputStream(u)) {
            BitmapFactory.decodeStream(in, null, o);
        }
        // 2. Smallest halving that brings the longest side down to GAL_MAX or less.
        int side = Math.max(o.outWidth, o.outHeight);
        int sample = 1;
        while (side > 0 && side / sample > GAL_MAX) {
            sample *= 2;
        }
        // 3. The pixels, at the reduced size. A stream can be read only once, so open it again.
        BitmapFactory.Options d = new BitmapFactory.Options();
        d.inSampleSize = sample;
        Bitmap bm;
        try (InputStream in = getContentResolver().openInputStream(u)) {
            bm = BitmapFactory.decodeStream(in, null, d);
        }
        // 4. Its own reader: the camera's reader may be busy on the analyser thread right now.
        try {
            return decodeBitmap(bm, new MultiFormatReader());
        } finally {
            if (bm != null) {
                bm.recycle();
            }
        }
    }

    /** Stops the frame-analysis thread when the screen goes away (the camera itself stops with the lifecycle). */
    @Override
    protected void onDestroy() {
        super.onDestroy();
        if (camExec != null) {
            camExec.shutdown();
        }
    }

    /**
     * Binds the back camera to this screen with a live preview and a frame analyser. Only the
     * newest frame is kept, so slow decoding never builds a queue. Binding to this
     * activity's lifecycle (CameraX) makes the camera stop and start with the screen, so
     * it is never left open in the background. Errors are written to the Journal (log).
     */
    private void startCam() {
        // The camera provider is created asynchronously; the listener runs on the main
        // thread once it is ready, because binding use cases must happen there.
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
                // Frames are decoded on a separate thread so the preview stays smooth.
                // One thread for the whole screen: startCam can run more than once
                // (permission result), and each call used to leak a new thread.
                if (camExec == null) {
                    camExec = Executors.newSingleThreadExecutor();
                }
                an.setAnalyzer(camExec, this::analyze);
                // Drop any earlier binding (e.g. from a previous startCam call) first.
                provider.unbindAll();
                provider.bindToLifecycle(this, CameraSelector.DEFAULT_BACK_CAMERA, preview, an);
            } catch (Exception e) {
                Journal.add("camera " + e.getMessage());
            }
        }, ContextCompat.getMainExecutor(this));
    }

    /**
     * Camera frame callback, run on a background thread. Skips work once a code was accepted
     * or while a found code is still on its way to the main thread, always closes the frame
     * so the camera can deliver the next one, and hands found text to accept() on the UI thread.
     * The {@link #pending} flag lets exactly one accept() call be queued at a time; it is
     * cleared just before accept() runs, and by then accept() has set {@link #got} if it took
     * the code, so scanning only resumes when the code was rejected (the user can retry).
     */
    private void analyze(ImageProxy image) {
        if (got.get() || pending.get()) {
            image.close();
            return;
        }
        try {
            String t = decodeY(image);
            if (t != null && !t.isEmpty() && pending.compareAndSet(false, true)) {
                runOnUiThread(() -> {
                    pending.set(false);
                    accept(t);
                });
            }
        } finally {
            image.close();
        }
    }

    /**
     * Decodes a QR code from the frame's luminance (Y) plane only, which is all
     * ZXing needs, turned into grey pixels. Row stride may be wider than the
     * image, so rows are copied one by one. Runs only on the analyser thread, which is
     * what makes reusing {@link #yBytes}, {@link #yPixels} and {@link #camReader} safe.
     */
    private String decodeY(ImageProxy image) {
        // Camera frames come as YUV; plane 0 is Y (brightness). Each grey value is
        // expanded into an opaque ARGB pixel with equal red, green and blue.
        ImageProxy.PlaneProxy y = image.getPlanes()[0];
        ByteBuffer buf = y.getBuffer();
        // Reuse the buffers from the previous frame when they are big enough (the
        // frame size normally never changes during a scan); allocate only when not.
        int n = buf.remaining();
        if (yBytes == null || yBytes.length < n) {
            yBytes = new byte[n];
        }
        byte[] data = yBytes;
        buf.get(data, 0, n);
        int w = image.getWidth();
        int h = image.getHeight();
        if (yPixels == null || yPixels.length != w * h) {
            yPixels = new int[w * h];
        }
        int[] pixels = yPixels;
        int row = y.getRowStride();
        for (int j = 0; j < h; j++) {
            for (int i = 0; i < w; i++) {
                int v = data[j * row + i] & 0xff;
                pixels[j * w + i] = 0xff000000 | (v << 16) | (v << 8) | v;
            }
        }
        // RGBLuminanceSource copies the pixels into its own array, so the buffer can be
        // reused for the next frame as soon as this call returns.
        return decodePixels(pixels, w, h, camReader);
    }

    /** Decodes a QR code from a still image (gallery) with the given reader, or returns null. */
    private static String decodeBitmap(Bitmap bm, MultiFormatReader reader) {
        if (bm == null) {
            return null;
        }
        int w = bm.getWidth();
        int h = bm.getHeight();
        int[] px = new int[w * h];
        bm.getPixels(px, 0, w, 0, 0, w, h);
        return decodePixels(px, w, h, reader);
    }

    /**
     * Runs ZXing on ARGB pixels and returns the code text, or null when nothing was found.
     * The reader is reset after every attempt because the camera's reader is reused for
     * every frame. Each caller passes a reader that no other thread is using at the same
     * time, because ZXing readers keep state and are not thread-safe.
     * ZXing reports "no code here" by throwing, which is the normal case for most
     * frames, so exceptions are ignored. Any barcode format with text is accepted,
     * not only QR codes.
     */
    private static String decodePixels(int[] px, int w, int h, MultiFormatReader reader) {
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
     * the list, the profiles are added. Runs on the main thread. The compareAndSet on
     * {@link #got} lets only the first of several quick calls through; every path that
     * does not close the screen clears it again so the user can retry.
     */
    private void accept(String raw) {
        if (raw == null || raw.trim().isEmpty() || !got.compareAndSet(false, true)) {
            return;
        }
        // 1. A TV pairing QR: send every profile to the TV and close on success.
        // PairUrl.send does the HTTP POST on its own thread and calls back on the main thread.
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
        // Must run after show(): the dialog's buttons exist only once it is shown.
        Fit.alertButtons(dlg);
    }
}
