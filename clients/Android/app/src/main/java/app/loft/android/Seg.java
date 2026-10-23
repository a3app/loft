package app.loft.android;

import android.view.View;
import android.view.ViewGroup;

final class Seg {
    interface Pick {
        void on(int index);
    }

    static int selected(ViewGroup row) {
        for (int i = 0; i < row.getChildCount(); i++) {
            if (row.getChildAt(i).isSelected()) {
                return i;
            }
        }
        return 0;
    }

    static void bind(ViewGroup row, int selected, Pick pick) {
        for (int i = 0; i < row.getChildCount(); i++) {
            View c = row.getChildAt(i);
            final int idx = i;
            c.setSelected(i == selected);
            c.setOnClickListener(v -> {
                for (int j = 0; j < row.getChildCount(); j++) {
                    row.getChildAt(j).setSelected(j == idx);
                }
                if (pick != null) {
                    pick.on(idx);
                }
            });
        }
    }
}
