/*
 * Android client, shared code (phone and TV builds), used by the phone home
 * screen. SparkView: the small live traffic chart on the main screen. It draws the
 * download (gold) and upload (grey) rates that LoftVpnService samples into
 * Live, either as smooth lines or as bars, and a pulsing text label while the
 * tunnel is still connecting.
 */
package app.loft.android;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.LinearGradient;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.Shader;
import android.util.AttributeSet;
import android.view.View;

/**
 * Recent ↓/↑ traffic chart (the last N = 40 samples, about 40 seconds):
 * smooth lines (default) or bars. While connecting it shows a pulsing
 * "handshake" label instead. A plain custom View that draws on the Canvas,
 * so no chart library is needed. The view keeps its own copy of the samples
 * (setSamples) instead of reading Live while drawing, so onDraw never sees
 * the arrays half-updated by the service.
 */
public final class SparkView extends View {
    // Number of samples kept; the service adds one per second. Live sizes its
    // history arrays from this value.
    static final int N = 40;

    // Paints and paths are created once and reused on every frame, because
    // allocating objects inside onDraw causes garbage collection pauses.
    /** Download line (gold) and upload line (grey). */
    private final Paint downStroke = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint upStroke = new Paint(Paint.ANTI_ALIAS_FLAG);
    /** Gradient area under the download line. */
    private final Paint fill = new Paint(Paint.ANTI_ALIAS_FLAG);
    /** Bars in bar mode. */
    private final Paint bar = new Paint(Paint.ANTI_ALIAS_FLAG);
    /** Dots marking the newest sample of each line. */
    private final Paint dot = new Paint(Paint.ANTI_ALIAS_FLAG);
    /** The handshake label. */
    private final Paint text = new Paint(Paint.ANTI_ALIAS_FLAG);
    /** Reused paths: download line, its filled area, upload line. */
    private final Path downPath = new Path();
    private final Path fillPath = new Path();
    private final Path upPath = new Path();

    /** Download and upload samples in bytes/s, oldest first. */
    private final float[] dn = new float[N];
    private final float[] up = new float[N];
    /** How many of the N slots hold real samples. */
    private int n;
    /** True to draw bars instead of lines. */
    private boolean bars;
    /** True while connecting: the label is drawn instead of the chart. */
    private boolean handshake;
    /** Text of the handshake label (for example "Connecting…"). */
    private String handshakeText = "";
    /** Phase of the label's fade animation, in radians. */
    private float pulse;
    /** True while the tick runnable is scheduled. */
    private boolean pulsing;

    /** Creates the view from code. */
    public SparkView(Context c) {
        super(c);
        init();
    }

    /** Creates the view from XML. */
    public SparkView(Context c, AttributeSet a) {
        super(c, a);
        init();
    }

    /** Creates the view from XML with a default style. */
    public SparkView(Context c, AttributeSet a, int s) {
        super(c, a, s);
        init();
    }

    /** Sets up the paints: colours, stroke widths and label size. */
    private void init() {
        downStroke.setStyle(Paint.Style.STROKE);
        downStroke.setStrokeWidth(dp(1.6f));
        downStroke.setStrokeCap(Paint.Cap.ROUND);
        downStroke.setStrokeJoin(Paint.Join.ROUND);
        downStroke.setColor(0xFFE8B84A);
        upStroke.setStyle(Paint.Style.STROKE);
        upStroke.setStrokeWidth(dp(1.2f));
        upStroke.setStrokeCap(Paint.Cap.ROUND);
        upStroke.setStrokeJoin(Paint.Join.ROUND);
        upStroke.setColor(0xFFA89A8C);
        fill.setStyle(Paint.Style.FILL);
        bar.setStyle(Paint.Style.FILL);
        dot.setStyle(Paint.Style.FILL);
        text.setColor(0xFF8A7E72);
        text.setTextSize(dp(12));
        text.setAntiAlias(true);
    }

    /** Switches between line chart (false) and bar chart (true). */
    void setBars(boolean on) {
        // Screens call the setters on every redraw; redraw the chart only when
        // something it shows actually changed.
        if (bars == on) {
            return;
        }
        bars = on;
        invalidate();
    }

    /** While connecting there is no traffic to draw, so the view shows a pulsing label instead. */
    void setHandshake(boolean on, String label) {
        String l = label == null ? "" : label;
        if (handshake == on && l.equals(handshakeText) && (!on || pulsing)) {
            return;
        }
        handshake = on;
        handshakeText = l;
        if (on) {
            startPulse();
        } else {
            pulsing = false;
        }
        invalidate();
    }

    /** Copies the latest rate samples (bytes/s) from Live. */
    void setSamples(float[] down, float[] up, int count) {
        int c = Math.max(0, Math.min(N, count));
        if (c == n && same(down, dn, c) && same(up, this.up, c)) {
            return;
        }
        n = c;
        if (down != null) {
            System.arraycopy(down, 0, dn, 0, n);
        }
        if (up != null) {
            System.arraycopy(up, 0, this.up, 0, n);
        }
        invalidate();
    }

    /** True when the first {@code c} values of the two arrays are equal (a null source never is). */
    private static boolean same(float[] src, float[] mine, int c) {
        if (src == null) {
            return false;
        }
        for (int i = 0; i < c; i++) {
            if (src[i] != mine[i]) {
                return false;
            }
        }
        return true;
    }

    /** Drops all samples and stops the handshake label, leaving an empty view. */
    void clear() {
        if (n == 0 && !handshake && !pulsing) {
            return;
        }
        n = 0;
        handshake = false;
        pulsing = false;
        invalidate();
    }

    /** Starts the label fade loop unless it is already running. */
    private void startPulse() {
        if (pulsing) {
            return;
        }
        pulsing = true;
        post(tick);
    }

    /**
     * Advances the fade about 25 times a second (every 40 ms); stops itself
     * when the handshake ends. Each step redraws the whole view.
     */
    private final Runnable tick = new Runnable() {
        @Override
        public void run() {
            if (!pulsing || !handshake) {
                pulsing = false;
                return;
            }
            pulse += 0.08f;
            if (pulse > Math.PI * 2) {
                pulse -= Math.PI * 2;
            }
            invalidate();
            postDelayed(this, 40);
        }
    };

    /** Stops the animation loop so a removed view is not redrawn. */
    @Override
    protected void onDetachedFromWindow() {
        pulsing = false;
        removeCallbacks(tick);
        super.onDetachedFromWindow();
    }

    /** Draws the handshake label, the bars or the lines, depending on state. */
    @Override
    protected void onDraw(Canvas c) {
        int w = getWidth();
        int h = getHeight();
        if (w <= 0 || h <= 0) {
            return;
        }
        if (handshake) {
            float a = 0.45f + 0.35f * (float) Math.sin(pulse);
            text.setAlpha((int) (255 * a));
            String s = handshakeText;
            float tw = text.measureText(s);
            c.drawText(s, (w - tw) / 2f, h * 0.62f, text);
            return;
        }
        if (bars) {
            drawBars(c, w, h);
            return;
        }
        if (n < 2) {
            return;
        }
        float max = peak();
        if (max <= 0) {
            return;
        }
        drawLines(c, w, h, max);
    }

    /** Largest sample in either direction; both series share this scale. */
    private float peak() {
        float max = 0;
        for (int i = 0; i < n; i++) {
            max = Math.max(max, dn[i]);
            max = Math.max(max, up[i]);
        }
        return max;
    }

    // Point buffers for drawLines, sized for the full window and reused every
    // frame, and the fill gradient, rebuilt only when the view height changes:
    // allocating them per frame fed the garbage collector for nothing.
    private final float[] dx = new float[N];
    private final float[] dy = new float[N];
    private final float[] ux = new float[N];
    private final float[] uy = new float[N];
    private LinearGradient grad;
    private int gradH = -1;

    // Download as a filled curve, upload as a thinner line, both scaled to the
    // common peak, with a dot on the newest sample.
    private void drawLines(Canvas c, int w, int h, float max) {
        pts(dn, max, w, h, dx, dy, 1f);
        pts(up, max, w, h, ux, uy, 1f);
        curve(downPath, dx, dy);
        fillPath.set(downPath);
        fillPath.lineTo(dx[n - 1], h);
        fillPath.lineTo(dx[0], h);
        fillPath.close();
        if (grad == null || gradH != h) {
            grad = new LinearGradient(0, 0, 0, h, 0x52E8B84A, 0x00E8B84A, Shader.TileMode.CLAMP);
            gradH = h;
        }
        fill.setShader(grad);
        c.drawPath(fillPath, fill);
        fill.setShader(null);
        c.drawPath(downPath, downStroke);
        curve(upPath, ux, uy);
        c.drawPath(upPath, upStroke);
        dot.setColor(0x2EE8B84A);
        c.drawCircle(dx[n - 1], dy[n - 1], dp(5), dot);
        dot.setColor(0xFFE8B84A);
        c.drawCircle(dx[n - 1], dy[n - 1], dp(2.4f), dot);
        dot.setColor(0xFFA89A8C);
        c.drawCircle(ux[n - 1], uy[n - 1], dp(1.9f), dot);
    }

    // Download bars above the middle, upload bars below. The loop computes a
    // fading opacity for older bars, but setColor() right after setAlpha()
    // replaces the alpha with the colour's own (fully opaque), so as written
    // all bars are drawn at full opacity. Zero samples draw a 1 px floor line.
    private void drawBars(Canvas c, int w, int h) {
        if (n <= 0) {
            return;
        }
        float max = peak();
        if (max <= 0) {
            max = 1;
        }
        float gap = dp(2);
        // Always a 40-slot window so the first samples stay thin, latest on the right.
        float bw = Math.max(dp(1), (w - gap * (N - 1)) / N);
        float gutter = dp(6);
        float rowH = (h - gutter) / 2f;
        float floor = Math.max(1f, dp(1f));
        int origin = N - n;
        for (int i = 0; i < n; i++) {
            float x = (origin + i) * (bw + gap);
            float op = 0.3f + 0.7f * ((origin + i) / (float) (N - 1));
            float y0 = rowH + gutter;
            bar.setAlpha((int) (255 * op));
            bar.setColor(0xFFE8B84A);
            if (dn[i] > 0) {
                float hd = Math.max(dp(2), (dn[i] / max) * rowH);
                c.drawRoundRect(x, rowH - hd, x + bw, rowH, dp(1.5f), dp(1.5f), bar);
            } else {
                c.drawRect(x, rowH - floor, x + bw, rowH, bar);
            }
            bar.setColor(0xFFA89A8C);
            if (up[i] > 0) {
                float hu = Math.max(dp(2), (up[i] / max) * rowH);
                c.drawRoundRect(x, y0 + rowH - hu, x + bw, y0 + rowH, dp(1.5f), dp(1.5f), bar);
            } else {
                c.drawRect(x, y0 + rowH - floor, x + bw, y0 + rowH, bar);
            }
        }
        bar.setAlpha(255);
    }

    /**
     * Converts samples to screen points. Points are placed in a fixed N-slot
     * grid aligned right, so the newest sample is always at the right edge.
     * {@code scale} stretches the values vertically (callers pass 1); y is
     * clamped to the view with a small top/bottom padding.
     */
    private void pts(float[] v, float max, int w, int h, float[] x, float[] y, float scale) {
        float padX = dp(2);
        float padY = dp(6);
        int origin = N - n;
        float span = (w - padX * 2) / (float) (N - 1);
        for (int i = 0; i < n; i++) {
            x[i] = padX + (origin + i) * span;
            float t = Math.max(0, Math.min(1, (v[i] / max) * scale));
            y[i] = h - padY - t * (h - padY * 2);
        }
    }

    // Smooth curve through the points (Catmull-Rom converted to cubic Béziers).
    // Catmull-Rom passes through every sample; each segment's two control
    // points come from the neighbouring samples, and the ends repeat the
    // first / last point.
    private void curve(Path path, float[] x, float[] y) {
        path.reset();
        path.moveTo(x[0], y[0]);
        for (int i = 1; i < n; i++) {
            float x0 = i >= 2 ? x[i - 2] : x[i - 1];
            float y0 = i >= 2 ? y[i - 2] : y[i - 1];
            float x1 = x[i - 1];
            float y1 = y[i - 1];
            float x2 = x[i];
            float y2 = y[i];
            float x3 = i + 1 < n ? x[i + 1] : x[i];
            float y3 = i + 1 < n ? y[i + 1] : y[i];
            path.cubicTo(
                    x1 + (x2 - x0) / 6f, y1 + (y2 - y0) / 6f,
                    x2 - (x3 - x1) / 6f, y2 - (y3 - y1) / 6f,
                    x2, y2);
        }
    }

    /** Converts dp (density-independent pixels) to screen pixels. */
    private float dp(float v) {
        return v * getResources().getDisplayMetrics().density;
    }
}
