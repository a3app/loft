/*
 * Android client, shared code (lives in src/main; used by the phone screens).
 * RevealFrame: the swipeable row container used by the connection list
 * (row_conn.xml, MainActivity) and the split-tunnel service lists
 * (row_split.xml, ListsActivity). It holds a front "card" view and a
 * "behind" layer with Edit/Delete actions that the card slides away to show.
 */
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
 * Connection row from the design mock-up: the card slides up to 84dp and
 * parks there, showing Edit on the left and Delete on the right. The row
 * never flies off the list (unlike the stock swipe-to-dismiss), so a delete
 * always needs a second, deliberate tap. The layout must contain a child
 * with id "card" (the sliding front) and may contain "behind", "act_edit",
 * "act_del", "mini" and "body"; missing ones are simply skipped.
 */
public final class RevealFrame extends FrameLayout {
    /** How far the card slides when parked open, in dp. */
    static final int REVEAL_DP = 84;
    /** Minimum drag distance, in dp, for a release to park the card open instead of closing it. */
    static final int THRESH_DP = 48;
    /** Duration of the snap animation, in milliseconds. */
    static final int ANIM_MS = 180;

    /** Callbacks the owning list uses to keep only one row open at a time. */
    interface Listener {
        /** Called when a horizontal drag starts on this row (so other open rows can close). */
        void onRevealStart(RevealFrame row);

        /** Called when the snap animation ends; {@code tx} is the final card offset in pixels. */
        void onSettled(RevealFrame row, float tx);
    }

    /** Owner callback (the list screen); null when nobody listens. */
    private Listener listener;
    /** Touch position at ACTION_DOWN, used to measure the drag. */
    private float x0;
    private float y0;
    /** Card translation when the touch started. */
    private float start;
    /** True once the gesture has been recognised as a horizontal swipe. */
    private boolean dragging;
    /** System touch slop: movement below this is still treated as a tap. */
    private int slop;
    /** REVEAL_DP and THRESH_DP converted to pixels. */
    private float reveal;
    private float thresh;
    /** Allowed card translation range; narrowed by leftOnly() and lock(). */
    private float minTx;
    private float maxTx;

    /** Creates a row from code. */
    public RevealFrame(Context c) {
        super(c);
        init(c);
    }

    /** Creates a row inflated from XML. */
    public RevealFrame(Context c, AttributeSet a) {
        super(c, a);
        init(c);
    }

    /** Creates a row inflated from XML with a style; the style is ignored (super(c, a) is called). */
    public RevealFrame(Context c, AttributeSet a, int defStyle) {
        super(c, a);
        init(c);
    }

    /** Converts the dp constants to pixels and sets the default two-way slide range. */
    private void init(Context c) {
        float d = c.getResources().getDisplayMetrics().density;
        reveal = REVEAL_DP * d;
        thresh = THRESH_DP * d;
        minTx = -reveal;
        maxTx = reveal;
        slop = ViewConfiguration.get(c).getScaledTouchSlop();
        // Clip the action layer, not this frame: clipping the card to the
        // same 12dp round eats its 1dp stroke and lets Edit/Delete tint
        // the outline through the corner AA.
        setClipToOutline(false);
        setClipChildren(false);
    }

    /** Gives the "behind" action layer rounded 12dp corners so it matches the card shape. */
    @Override
    protected void onSizeChanged(int w, int h, int oldw, int oldh) {
        super.onSizeChanged(w, h, oldw, oldh);
        // Done here rather than in init() because children only exist after
        // inflation, and the outline needs the final size anyway.
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

    /** Sets the callback for drag start and settle events (null to clear). */
    void setListener(Listener l) {
        listener = l;
    }

    /** List rows only swipe left (Delete / Restore). */
    void leftOnly() {
        minTx = -reveal;
        maxTx = 0f;
    }

    /** Selection mode: the card does not slide. */
    void lock() {
        minTx = 0f;
        maxTx = 0f;
        settleNow(0);
    }

    /** The front view that slides; null if the layout has no R.id.card. */
    View card() {
        return findViewById(R.id.card);
    }

    /** Current horizontal offset of the card in pixels (0 when closed). */
    float translation() {
        View c = card();
        return c == null ? 0 : c.getTranslationX();
    }

    /**
     * Moves the card to {@code tx} instantly, without animation or callback.
     * Used when a recycled row is rebound and must show its saved state.
     */
    void settleNow(float tx) {
        View c = card();
        if (c == null) {
            return;
        }
        c.animate().cancel();
        c.setTranslationX(tx);
        syncActions();
    }

    /**
     * Animates the card to {@code tx} and reports the final offset to the
     * listener. The curve is an ease-out (fast start, gentle stop). The
     * update listener keeps the Edit/Delete visibility in step on every frame.
     */
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

    /**
     * Shows the Edit/Delete buttons only while the card is dragged or moved
     * off centre, so they never peek out around a closed card.
     */
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

    /** Animates the card back to the closed position. */
    void close() {
        snap(0);
    }

    /** True when the touch is inside the card at its current (slid) position. */
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

    /**
     * True when the touch is on the small Connect button (R.id.mini) inside the
     * card. Touches there are left to the button so a tap is not taken as a swipe.
     */
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

    /**
     * Lets child views (card body, buttons) receive taps, and takes over the
     * gesture only once it becomes a mostly horizontal move past the touch slop.
     */
    @Override
    public boolean onInterceptTouchEvent(MotionEvent e) {
        int act = e.getActionMasked();
        // Never intercept on DOWN: children must see the start of a tap.
        // Only record the start point when the touch begins on the card.
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
        // Returning true here steals the rest of the gesture from the
        // children (they get ACTION_CANCEL) and sends it to onTouchEvent.
        if (act == MotionEvent.ACTION_MOVE) {
            if (onMini(e) && !dragging) {
                return false;
            }
            float dx = e.getX() - x0;
            float dy = e.getY() - y0;
            if (Math.abs(dx) > slop && Math.abs(dx) > Math.abs(dy) && maxTx > minTx) {
                beginDrag();
                return true;
            }
        }
        return false;
    }

    /**
     * Drives the swipe: the card follows the finger within the allowed range,
     * and on release it parks open if dragged past the threshold, else closes.
     */
    @Override
    public boolean onTouchEvent(MotionEvent e) {
        View c = card();
        if (c == null) {
            return super.onTouchEvent(e);
        }
        int act = e.getActionMasked();
        // 1. Finger down: remember where the gesture and the card started.
        if (act == MotionEvent.ACTION_DOWN) {
            x0 = e.getX();
            y0 = e.getY();
            start = c.getTranslationX();
            return true;
        }
        // 2. Move: start the drag once it is clearly horizontal, then follow the finger.
        if (act == MotionEvent.ACTION_MOVE) {
            float dx = e.getX() - x0;
            float dy = e.getY() - y0;
            if (!dragging && Math.abs(dx) > slop && Math.abs(dx) > Math.abs(dy) && maxTx > minTx) {
                beginDrag();
            }
            if (dragging) {
                c.setTranslationX(clamp(start + dx, minTx, maxTx));
                syncActions();
            }
            return true;
        }
        // 3. Release: snap open on the dragged side if past the threshold, otherwise close.
        if (act == MotionEvent.ACTION_UP || act == MotionEvent.ACTION_CANCEL) {
            if (dragging) {
                dragging = false;
                float x = c.getTranslationX();
                float target = 0;
                if (x > thresh && maxTx > 0) {
                    target = maxTx;
                } else if (x < -thresh && minTx < 0) {
                    target = minTx;
                }
                snap(target);
                return true;
            }
        }
        return super.onTouchEvent(e);
    }

    /**
     * Marks the gesture as a swipe: stops the parent list from scrolling,
     * cancels pending long-presses (which would open selection mode) and
     * tells the listener so other open rows can close.
     */
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

    /**
     * Sizes the row by the card alone, then stretches the other children (the
     * action layer) to exactly the card's height so they never make the row taller.
     */
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
                // Height is the card's own height, ignoring the parent's spec.
                h + getPaddingTop() + getPaddingBottom());
    }

    /** Limits {@code v} to the range [lo, hi]. */
    static float clamp(float v, float lo, float hi) {
        return Math.max(lo, Math.min(hi, v));
    }
}
