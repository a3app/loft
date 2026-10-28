/*
 * Android client, shared code: a one-method helper that both UI flavors
 * (phone, TV) use to open their bottom-sheet dialogs with the same theme.
 */
package app.loft.android;

import android.content.Context;
import android.graphics.Color;
import android.view.LayoutInflater;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;

import androidx.core.graphics.Insets;
import androidx.core.view.ViewCompat;
import androidx.core.view.WindowInsetsCompat;

import com.google.android.material.bottomsheet.BottomSheetBehavior;
import com.google.android.material.bottomsheet.BottomSheetDialog;

/** Builds a bottom sheet dialog with the app's sheet theme from a layout resource. */
final class Sheets {
    /** Inflates {@code layout} into a new bottom sheet; the caller finds its views and calls show(). */
    static BottomSheetDialog of(Context c, int layout) {
        return inflate(c, layout, R.style.Theme_Loft_Sheet);
    }

    /**
     * iOS-style action sheet: two floating cards over a 50% dim, not an
     * edge-to-edge Material sheet. The host is stretched full-screen so the
     * window dim covers the tab bar; the layout pins the cards to the bottom.
     */
    static BottomSheetDialog cards(Context c, int layout) {
        BottomSheetDialog d = inflate(c, layout, R.style.Theme_Loft_Sheet_Cards);
        d.setOnShowListener(dlg -> {
            FrameLayout sheet = d.findViewById(
                    com.google.android.material.R.id.design_bottom_sheet);
            if (sheet != null) {
                sheet.setBackgroundTintList(null);
                sheet.setBackgroundColor(Color.TRANSPARENT);
                ViewGroup.LayoutParams lp = sheet.getLayoutParams();
                lp.height = ViewGroup.LayoutParams.MATCH_PARENT;
                sheet.setLayoutParams(lp);
                BottomSheetBehavior<FrameLayout> b = BottomSheetBehavior.from(sheet);
                b.setSkipCollapsed(true);
                b.setFitToContents(false);
                b.setDraggable(false);
                b.setState(BottomSheetBehavior.STATE_EXPANDED);
            }
            View scrim = d.findViewById(R.id.sheet_scrim);
            if (scrim != null) {
                scrim.setOnClickListener(v -> d.dismiss());
            }
            // Mock 28px is from the bezel and includes the home indicator.
            // The dialog already sits above the system nav; 8dp matches the
            // gap from Cancel to the indicator so the plate covers tab labels.
            View column = d.findViewById(R.id.sheet_cards);
            if (column != null) {
                ViewCompat.setOnApplyWindowInsetsListener(column, (v, insets) -> {
                    Insets nav = insets.getInsets(WindowInsetsCompat.Type.navigationBars());
                    float dp = v.getResources().getDisplayMetrics().density;
                    int side = Math.round(16 * dp);
                    int bottom = Math.round(8 * dp) + nav.bottom;
                    v.setPadding(side, 0, side, bottom);
                    return insets;
                });
                ViewCompat.requestApplyInsets(column);
            }
        });
        return d;
    }

    private static BottomSheetDialog inflate(Context c, int layout, int theme) {
        BottomSheetDialog d = new BottomSheetDialog(c, theme);
        View v = LayoutInflater.from(c).inflate(layout, null);
        d.setContentView(v);
        return d;
    }
}
