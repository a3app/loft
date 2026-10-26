package app.loft.android;

import android.content.Context;
import android.graphics.Outline;
import android.util.AttributeSet;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.ViewOutlineProvider;
import android.view.ViewParent;
import android.view.animation.PathInterpolator;
import android.widget.FrameLayout;

/**
 * Connection row from the mock: the card slides up to 84dp and parks
 * there, showing Edit on the left and Delete on the right. The row
 * never flies off the list.
 */
public final class RevealFrame extends FrameLayout {
    static final int REVEAL_DP = 84;
    static final int THRESH_DP = 48;
    static final int ANIM_MS = 180;

    interface Listener {
        void onRevealStart(RevealFrame row);

        void onSettled(RevealFrame row, float tx);
    }

    private Listener listener;
    private float x0;
    private float y0;
    private float start;
    private boolean dragging;
    private int slop;
    private float reveal;
    private float thresh;

    public RevealFrame(Context c) {
        super(c);
        init(c);
    }

    public RevealFrame(Context c, AttributeSet a) {
        super(c, a);
        init(c);
    }

    public RevealFrame(Context c, AttributeSet a, int defStyle) {
        super(c, a);
        init(c);
    }

    private void init(Context c) {
        float d = c.getResources().getDisplayMetrics().density;
        reveal = REVEAL_DP * d;
        thresh = THRESH_DP * d;
        slop = ViewConfiguration.get(c).getScaledTouchSlop();
        // Clip the action layer, not this frame: clipping the card to the
        // same 12dp round eats its 1dp stroke and lets Edit/Delete tint
        // the outline through the corner AA.
        setClipToOutline(false);
        setClipChildren(false);
    }

    @Override
    protected void onSizeChanged(int w, int h, int oldw, int oldh) {
        super.onSizeChanged(w, h, oldw, oldh);
        View behind = findViewById(R.id.behind);
        if (behind == null) {
            return;
        }
        final float r = 12f * getResources().getDisplayMetrics().density;
        behind.setOutlineProvider(new ViewOutlineProvider() {
            @Override
            public void getOutline(View view, Outline outline) {
                outline.setRoundRect(0, 0, view.getWidth(), view.getHeight(), r);
            }
        });
        behind.setClipToOutline(true);
    }

    void setListener(Listener l) {
        listener = l;
    }

    View card() {
        return findViewById(R.id.card);
    }

    float translation() {
        View c = card();
        return c == null ? 0 : c.getTranslationX();
    }

    void settleNow(float tx) {
        View c = card();
        if (c == null) {
            return;
        }
        c.animate().cancel();
        c.setTranslationX(tx);
        syncActions();
    }

    void snap(float tx) {
        View c = card();
        if (c == null) {
            return;
        }
        c.animate().cancel();
        c.animate()
                .translationX(tx)
                .setDuration(ANIM_MS)
                .setInterpolator(new PathInterpolator(0.25f, 0.1f, 0.25f, 1f))
                .setUpdateListener(a -> syncActions())
                .withEndAction(() -> {
                    syncActions();
                    if (listener != null) {
                        listener.onSettled(this, tx);
                    }
                })
                .start();
    }

    private void syncActions() {
        View c = card();
        boolean show = dragging || (c != null && Math.abs(c.getTranslationX()) > 0.5f);
        int vis = show ? VISIBLE : INVISIBLE;
        View edit = findViewById(R.id.act_edit);
        View del = findViewById(R.id.act_del);
        if (edit != null) {
            edit.setVisibility(vis);
        }
        if (del != null) {
            del.setVisibility(vis);
        }
    }

    void close() {
        snap(0);
    }

    private boolean onCard(MotionEvent e) {
        View c = card();
        if (c == null) {
            return false;
        }
        float x = e.getX();
        float y = e.getY();
        float tx = c.getTranslationX();
        return x >= c.getLeft() + tx && x < c.getRight() + tx
                && y >= c.getTop() && y < c.getBottom();
    }

    private boolean onMini(MotionEvent e) {
        View mini = findViewById(R.id.mini);
        if (mini == null || mini.getVisibility() != VISIBLE) {
            return false;
        }
        int[] self = new int[2];
        int[] m = new int[2];
        getLocationOnScreen(self);
        mini.getLocationOnScreen(m);
        float sx = e.getRawX();
        float sy = e.getRawY();
        return sx >= m[0] && sx < m[0] + mini.getWidth()
                && sy >= m[1] && sy < m[1] + mini.getHeight();
    }

    @Override
    public boolean onInterceptTouchEvent(MotionEvent e) {
        int act = e.getActionMasked();
        if (act == MotionEvent.ACTION_DOWN) {
            if (!onCard(e) || onMini(e)) {
                dragging = false;
                return false;
            }
            x0 = e.getX();
            y0 = e.getY();
            View c = card();
            start = c == null ? 0 : c.getTranslationX();
            dragging = false;
            return false;
        }
        if (act == MotionEvent.ACTION_MOVE) {
            if (onMini(e) && !dragging) {
                return false;
            }
            float dx = e.getX() - x0;
            float dy = e.getY() - y0;
            if (Math.abs(dx) > slop && Math.abs(dx) > Math.abs(dy)) {
                beginDrag();
                return true;
            }
        }
        return false;
    }

    @Override
    public boolean onTouchEvent(MotionEvent e) {
        View c = card();
        if (c == null) {
            return super.onTouchEvent(e);
        }
        int act = e.getActionMasked();
        if (act == MotionEvent.ACTION_DOWN) {
            x0 = e.getX();
            y0 = e.getY();
            start = c.getTranslationX();
            return true;
        }
        if (act == MotionEvent.ACTION_MOVE) {
            float dx = e.getX() - x0;
            float dy = e.getY() - y0;
            if (!dragging && Math.abs(dx) > slop && Math.abs(dx) > Math.abs(dy)) {
                beginDrag();
            }
            if (dragging) {
                c.setTranslationX(clamp(start + dx, -reveal, reveal));
                syncActions();
            }
            return true;
        }
        if (act == MotionEvent.ACTION_UP || act == MotionEvent.ACTION_CANCEL) {
            if (dragging) {
                dragging = false;
                float x = c.getTranslationX();
                float target = 0;
                if (x > thresh) {
                    target = reveal;
                } else if (x < -thresh) {
                    target = -reveal;
                }
                snap(target);
                return true;
            }
        }
        return super.onTouchEvent(e);
    }

    private void beginDrag() {
        dragging = true;
        ViewParent p = getParent();
        if (p != null) {
            p.requestDisallowInterceptTouchEvent(true);
        }
        View c = card();
        if (c != null) {
            c.cancelLongPress();
        }
        View body = findViewById(R.id.body);
        if (body != null) {
            body.cancelLongPress();
        }
        cancelLongPress();
        syncActions();
        if (listener != null) {
            listener.onRevealStart(this);
        }
    }

    @Override
    protected void onMeasure(int widthMeasureSpec, int heightMeasureSpec) {
        View card = card();
        if (card == null) {
            super.onMeasure(widthMeasureSpec, heightMeasureSpec);
            return;
        }
        measureChildWithMargins(card, widthMeasureSpec, 0, heightMeasureSpec, 0);
        int h = card.getMeasuredHeight();
        int hSpec = MeasureSpec.makeMeasureSpec(h, MeasureSpec.EXACTLY);
        int n = getChildCount();
        for (int i = 0; i < n; i++) {
            View ch = getChildAt(i);
            if (ch == card) {
                continue;
            }
            measureChildWithMargins(ch, widthMeasureSpec, 0, hSpec, 0);
        }
        setMeasuredDimension(
                resolveSize(card.getMeasuredWidth() + getPaddingLeft() + getPaddingRight(),
                        widthMeasureSpec),
                h + getPaddingTop() + getPaddingBottom());
    }

    static float clamp(float v, float lo, float hi) {
        return Math.max(lo, Math.min(hi, v));
    }
}
