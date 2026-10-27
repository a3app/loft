/*
 * Android client, shared code: a one-method helper that both UI flavors
 * (phone, TV) use to open their bottom-sheet dialogs with the same theme.
 */
package app.loft.android;

import android.content.Context;
import android.view.LayoutInflater;
import android.view.View;

import com.google.android.material.bottomsheet.BottomSheetDialog;

/** Builds a bottom sheet dialog with the app's sheet theme from a layout resource. */
final class Sheets {
    /** Inflates {@code layout} into a new bottom sheet; the caller finds its views and calls show(). */
    static BottomSheetDialog of(Context c, int layout) {
        BottomSheetDialog d = new BottomSheetDialog(c, R.style.Theme_Loft_Sheet);
        View v = LayoutInflater.from(c).inflate(layout, null);
        d.setContentView(v);
        return d;
    }
}
