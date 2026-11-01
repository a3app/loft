/*
 * Android client, shared code: a small helper that turns a row of plain
 * views into a segmented control (pick one of several options). Used by the
 * connection editor and onboarding screens in the phone build.
 */
package app.loft.android;

import android.view.View;
import android.view.ViewGroup;

/**
 * Segmented control built from plain views: a row whose children act like
 * radio buttons. The chosen child has the "selected" state, which the
 * drawables use for highlighting.
 */
final class Seg {
    /** Receives the index of the child the user tapped. */
    interface Pick {
        /** Called after the tapped child has been marked selected. */
        void on(int index);
    }

    /** Index of the selected child, or 0 (the first option) when none is selected. */
    static int selected(ViewGroup row) {
        for (int i = 0; i < row.getChildCount(); i++) {
            if (row.getChildAt(i).isSelected()) {
                return i;
            }
        }
        return 0;
    }

    /**
     * Selects child {@code selected} and makes every child select itself on
     * tap and report its index. Replaces any click listeners the children
     * had. Call again after adding or removing children.
     */
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
