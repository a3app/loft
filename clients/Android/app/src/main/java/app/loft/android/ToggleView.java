/*
 * Android client, shared code: the app's own on/off switch widget, drawn by
 * hand to match the design mock (a pill track with a round thumb) instead
 * of the Material switch. Used in the settings screens.
 */
package app.loft.android;

import android.animation.ValueAnimator;
import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.util.AttributeSet;
import android.view.animation.DecelerateInterpolator;
import android.widget.CompoundButton;

/**
 * The ".toggle" switch from the design mock-up: a 42×26 dp pill track that
 * turns lamp-gold when on, with a 20 dp round thumb inset 3 dp from the
 * edges. It extends CompoundButton, so checked state, click-to-toggle,
 * OnCheckedChangeListener and accessibility come from Android for free; only
 * the look is custom. Colour names (lamp, slate, ink, paper) are the app's
 * palette in res/values/colors.xml.
 */
public final class ToggleView extends CompoundButton {
    /** One paint reused for both track and thumb (only the colour changes). */
    private final Paint fill = new Paint(Paint.ANTI_ALIAS_FLAG);
    /** Reused track rectangle, so onDraw allocates nothing. */
    private final RectF track = new RectF();
    /** Colours: track off/on (slate/lamp) and thumb off/on (paper/ink). */
    private final int lamp;
    private final int slate;
    private final int ink;
    private final int paper;
    /** Thumb position from 0 (off) to 1 (on); animated between the two. */
    private float pos;
    /** Running slide animation, if any. */
    private ValueAnimator anim;

    /** Creates the switch from code; reads the palette colours once. */
    public ToggleView(Context c) {
        super(c);
        lamp = c.getColor(R.color.lamp);
        slate = c.getColor(R.color.slate);
        ink = c.getColor(R.color.ink);
        paper = c.getColor(R.color.paper);
        init();
    }

    /** Constructor used when the view is inflated from XML. */
    public ToggleView(Context c, AttributeSet a) {
        super(c, a);
        lamp = c.getColor(R.color.lamp);
        slate = c.getColor(R.color.slate);
        ink = c.getColor(R.color.ink);
        paper = c.getColor(R.color.paper);
        init();
    }

    /** XML constructor with a default style attribute. */
    public ToggleView(Context c, AttributeSet a, int s) {
        super(c, a, s);
        lamp = c.getColor(R.color.lamp);
        slate = c.getColor(R.color.slate);
        ink = c.getColor(R.color.ink);
        paper = c.getColor(R.color.paper);
        init();
    }

    /** Strips the default button look so only onDraw paints, and places the thumb for the current state. */
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

    // Android calls this when the view should show its state without animation,
    // for example when it is attached to a window or its state is restored;
    // snap the thumb without animating.
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

    // Slides the thumb when the state changes on screen; before layout it just
    // jumps, because a view that is not laid out yet has nothing to animate.
    // Note: CompoundButton's own constructor may call this before the colour
    // fields are set; that is fine because nothing here reads them.
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

    /** Always 42x26 dp, whatever the parent offers. */
    @Override
    protected void onMeasure(int wSpec, int hSpec) {
        float d = getResources().getDisplayMetrics().density;
        setMeasuredDimension(Math.round(42 * d), Math.round(26 * d));
    }

    /**
     * Draws the track (colour blends from slate to lamp with pos) and the
     * thumb (paper to ink) at its current position. The text a
     * CompoundButton would normally draw is skipped entirely.
     */
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

    // Linear blend of two ARGB colours, channel by channel: t = 0 gives a,
    // t = 1 gives b.
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
