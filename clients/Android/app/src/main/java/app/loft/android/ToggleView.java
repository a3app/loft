package app.loft.android;

import android.animation.ValueAnimator;
import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.util.AttributeSet;
import android.view.animation.DecelerateInterpolator;
import android.widget.CompoundButton;

/** Mock .toggle: 42×26 lamp pill, 20dp thumb, 3dp inset. */
public final class ToggleView extends CompoundButton {
    private final Paint fill = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final RectF track = new RectF();
    private final int lamp;
    private final int slate;
    private final int ink;
    private final int paper;
    private float pos;
    private ValueAnimator anim;

    public ToggleView(Context c) {
        super(c);
        lamp = c.getColor(R.color.lamp);
        slate = c.getColor(R.color.slate);
        ink = c.getColor(R.color.ink);
        paper = c.getColor(R.color.paper);
        init();
    }

    public ToggleView(Context c, AttributeSet a) {
        super(c, a);
        lamp = c.getColor(R.color.lamp);
        slate = c.getColor(R.color.slate);
        ink = c.getColor(R.color.ink);
        paper = c.getColor(R.color.paper);
        init();
    }

    public ToggleView(Context c, AttributeSet a, int s) {
        super(c, a, s);
        lamp = c.getColor(R.color.lamp);
        slate = c.getColor(R.color.slate);
        ink = c.getColor(R.color.ink);
        paper = c.getColor(R.color.paper);
        init();
    }

    private void init() {
        setClickable(true);
        setFocusable(true);
        setButtonDrawable(null);
        setBackground(null);
        setMinWidth(0);
        setMinHeight(0);
        setPadding(0, 0, 0, 0);
        setIncludeFontPadding(false);
        pos = isChecked() ? 1f : 0f;
    }

    @Override
    public void jumpDrawablesToCurrentState() {
        super.jumpDrawablesToCurrentState();
        if (anim != null) {
            anim.cancel();
            anim = null;
        }
        pos = isChecked() ? 1f : 0f;
        invalidate();
    }

    @Override
    public void setChecked(boolean checked) {
        boolean change = checked != isChecked();
        super.setChecked(checked);
        if (!change) {
            return;
        }
        if (!isLaidOut()) {
            pos = checked ? 1f : 0f;
            invalidate();
            return;
        }
        if (anim != null) {
            anim.cancel();
        }
        anim = ValueAnimator.ofFloat(pos, checked ? 1f : 0f);
        anim.setDuration(160);
        anim.setInterpolator(new DecelerateInterpolator());
        anim.addUpdateListener(a -> {
            pos = (float) a.getAnimatedValue();
            invalidate();
        });
        anim.start();
    }

    @Override
    protected void onMeasure(int wSpec, int hSpec) {
        float d = getResources().getDisplayMetrics().density;
        setMeasuredDimension(Math.round(42 * d), Math.round(26 * d));
    }

    @Override
    protected void onDraw(Canvas c) {
        float w = getWidth();
        float h = getHeight();
        float d = getResources().getDisplayMetrics().density;
        track.set(0, 0, w, h);
        fill.setColor(mix(slate, lamp, pos));
        c.drawRoundRect(track, h / 2f, h / 2f, fill);
        float r = 10f * d;
        float pad = 3f * d;
        float cx = pad + r + pos * (w - 2f * pad - 2f * r);
        fill.setColor(mix(paper, ink, pos));
        c.drawCircle(cx, h / 2f, r, fill);
    }

    private static int mix(int a, int b, float t) {
        t = Math.max(0f, Math.min(1f, t));
        int aa = (a >> 24) & 255, ar = (a >> 16) & 255, ag = (a >> 8) & 255, ab = a & 255;
        int ba = (b >> 24) & 255, br = (b >> 16) & 255, bg = (b >> 8) & 255, bb = b & 255;
        return ((int) (aa + (ba - aa) * t) << 24)
                | ((int) (ar + (br - ar) * t) << 16)
                | ((int) (ag + (bg - ag) * t) << 8)
                | (int) (ab + (bb - ab) * t);
    }
}
