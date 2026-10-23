package app.loft.android;

import android.content.Context;
import android.view.LayoutInflater;
import android.view.View;

import com.google.android.material.bottomsheet.BottomSheetDialog;

final class Sheets {
    static BottomSheetDialog of(Context c, int layout) {
        BottomSheetDialog d = new BottomSheetDialog(c, R.style.Theme_Loft_Sheet);
        View v = LayoutInflater.from(c).inflate(layout, null);
        d.setContentView(v);
        return d;
    }
}
