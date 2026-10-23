package app.loft.android;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.LinearGradient;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.Shader;
import android.util.AttributeSet;
import android.view.View;

/** Last-minute ↓/↑ chart: smooth lines (default) or fading bars. Handshake while connecting. */
public final class SparkView extends View {
    static final int N = 40;

    private final Paint downStroke = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint upStroke = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint fill = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint bar = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint dot = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint text = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Path downPath = new Path();
    private final Path fillPath = new Path();
    private final Path upPath = new Path();

    private final float[] dn = new float[N];
    private final float[] up = new float[N];
    private int n;
    private boolean bars;
    private boolean handshake;
    private String handshakeText = "";
    private float pulse;
    private boolean pulsing;

    public SparkView(Context c) {
        super(c);
        init();
    }

    public SparkView(Context c, AttributeSet a) {
        super(c, a);
        init();
    }

    public SparkView(Context c, AttributeSet a, int s) {
        super(c, a, s);
        init();
    }

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

    void setBars(boolean on) {
        bars = on;
        invalidate();
    }

    void setHandshake(boolean on, String label) {
        handshake = on;
        handshakeText = label == null ? "" : label;
        if (on) {
            startPulse();
        } else {
            pulsing = false;
        }
        invalidate();
    }

    void setSamples(float[] down, float[] up, int count) {
        n = Math.max(0, Math.min(N, count));
        if (down != null) {
            System.arraycopy(down, 0, dn, 0, n);
        }
        if (up != null) {
            System.arraycopy(up, 0, this.up, 0, n);
        }
        invalidate();
    }

    void clear() {
        n = 0;
        handshake = false;
        pulsing = false;
        invalidate();
    }

    private void startPulse() {
        if (pulsing) {
            return;
        }
        pulsing = true;
        post(tick);
    }

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

    @Override
    protected void onDetachedFromWindow() {
        pulsing = false;
        removeCallbacks(tick);
        super.onDetachedFromWindow();
    }

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

    private float peak() {
        float max = 0;
        for (int i = 0; i < n; i++) {
            max = Math.max(max, dn[i]);
            max = Math.max(max, up[i]);
        }
        return max;
    }

    private void drawLines(Canvas c, int w, int h, float max) {
        float[] dx = new float[n];
        float[] dy = new float[n];
        float[] ux = new float[n];
        float[] uy = new float[n];
        pts(dn, max, w, h, dx, dy, 1f);
        pts(up, max, w, h, ux, uy, 1f);
        curve(downPath, dx, dy);
        fillPath.set(downPath);
        fillPath.lineTo(dx[n - 1], h);
        fillPath.lineTo(dx[0], h);
        fillPath.close();
        fill.setShader(new LinearGradient(0, 0, 0, h,
                0x52E8B84A, 0x00E8B84A, Shader.TileMode.CLAMP));
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

    private float dp(float v) {
        return v * getResources().getDisplayMetrics().density;
    }
}
