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

/** Shared reflow for side-by-side actions at a large system font. */
final class Fit {
    private Fit() {
    }

    /**
     * Side-by-side children share the row height. If their unconstrained
     * widths overflow the row, they stack full-width so captions stay whole.
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

    /** Dialog actions wrap on two lines and keep sentence case. */
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
