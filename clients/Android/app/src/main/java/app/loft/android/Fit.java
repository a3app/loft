/*
 * Android client, shared code: large-system-font layout helpers. Button
 * rows keep equal height, then stack when the captions no longer fit;
 * AlertDialog actions wrap instead of ellipsizing.
 */
package app.loft.android;

import android.app.AlertDialog;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.LinearLayout;

/**
 * Shared reflow for side-by-side actions at a large system font. Users can
 * raise the font scale in Android settings; fixed two-button rows then cut
 * captions off ("Disc…"). These helpers adapt the layout instead.
 */
final class Fit {
    /** Static helpers only; never instantiated. */
    private Fit() {
    }

    /**
     * Side-by-side children share the row height. If their unconstrained
     * widths overflow the row, they stack full-width so captions stay whole.
     * The check is posted to run after the next layout pass, when the row's
     * real width is known. Stacking is one-way: the row is not switched back
     * to horizontal if it later becomes wide enough.
     */
    static void buttonRow(LinearLayout row) {
        if (row == null) {
            return;
        }
        row.post(() -> {
            int inner = row.getWidth();
            if (inner <= 0) {
                return;
            }
            // 1. Measure each child at its natural size (no width limit) and add
            //    up the widths plus side margins; remember the tallest.
            int n = row.getChildCount();
            int need = 0;
            int maxH = 0;
            for (int i = 0; i < n; i++) {
                View c = row.getChildAt(i);
                LinearLayout.LayoutParams lp = (LinearLayout.LayoutParams) c.getLayoutParams();
                c.measure(
                        View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED),
                        View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED));
                need += c.getMeasuredWidth() + lp.leftMargin + lp.rightMargin;
                maxH = Math.max(maxH, c.getMeasuredHeight());
            }
            // 2. Everything fits: give all children the same height.
            if (need <= inner) {
                for (int i = 0; i < n; i++) {
                    View c = row.getChildAt(i);
                    LinearLayout.LayoutParams lp = (LinearLayout.LayoutParams) c.getLayoutParams();
                    if (lp.height != maxH) {
                        lp.height = maxH;
                        c.setLayoutParams(lp);
                    }
                }
                return;
            }
            // 3. Too wide: stack vertically, full width, 6 dp apart.
            row.setOrientation(LinearLayout.VERTICAL);
            int gap = Math.round(6f * row.getResources().getDisplayMetrics().density);
            for (int i = 0; i < n; i++) {
                View c = row.getChildAt(i);
                LinearLayout.LayoutParams lp = (LinearLayout.LayoutParams) c.getLayoutParams();
                lp.width = ViewGroup.LayoutParams.MATCH_PARENT;
                lp.height = ViewGroup.LayoutParams.WRAP_CONTENT;
                lp.weight = 0;
                lp.leftMargin = 0;
                lp.rightMargin = 0;
                lp.setMarginStart(0);
                lp.setMarginEnd(0);
                lp.topMargin = i == 0 ? 0 : gap;
                c.setLayoutParams(lp);
            }
        });
    }

    /**
     * Dialog actions wrap on two lines and keep sentence case. Must be called
     * after the dialog is shown: getButton() returns null before that, and
     * such buttons are skipped. setTransformationMethod(null) removes the
     * theme's all-caps transform, which setAllCaps(false) alone may not undo.
     */
    static void alertButtons(AlertDialog d) {
        int[] which = {
                AlertDialog.BUTTON_POSITIVE,
                AlertDialog.BUTTON_NEGATIVE,
                AlertDialog.BUTTON_NEUTRAL,
        };
        for (int w : which) {
            Button b = d.getButton(w);
            if (b == null) {
                continue;
            }
            b.setAllCaps(false);
            b.setSingleLine(false);
            b.setMaxLines(2);
            b.setEllipsize(null);
            b.setTransformationMethod(null);
            b.setGravity(Gravity.END | Gravity.CENTER_VERTICAL);
        }
    }
}
