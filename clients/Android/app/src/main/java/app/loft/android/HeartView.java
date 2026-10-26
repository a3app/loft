package app.loft.android;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.util.AttributeSet;
import android.view.View;

/**
 * A³ colophon heart: 7×6 lamp cells, nearest-neighbour, no AA.
 * The small pixel heart in the About screen's "made with" line. Drawn as
 * whole square cells without anti-aliasing so the edges stay crisp at any
 * size. Each CELLS row is {x, y, width, height} in cells.
 */
public final class HeartView extends View {
    private static final int[][] CELLS = {
        {1, 0, 2, 1}, {4, 0, 2, 1},
        {0, 1, 7, 2},
        {1, 3, 5, 1},
        {2, 4, 3, 1},
        {3, 5, 1, 1},
    };

    private final Paint fill = new Paint(0);

    public HeartView(Context c) {
        super(c);
        init();
    }

    public HeartView(Context c, AttributeSet a) {
        super(c, a);
        init();
    }

    public HeartView(Context c, AttributeSet a, int s) {
        super(c, a, s);
        init();
    }

    private void init() {
        setWillNotDraw(false);
        setLayerType(LAYER_TYPE_SOFTWARE, null);
        fill.setAntiAlias(false);
        fill.setDither(false);
        fill.setFilterBitmap(false);
        fill.setStyle(Paint.Style.FILL);
        fill.setColor(getContext().getColor(R.color.lamp));
    }

    @Override
    protected void onDraw(Canvas c) {
        int w = getWidth();
        int h = getHeight();
        int cell = Math.min(w / 7, h / 6);
        if (cell < 1) {
            cell = 1;
        }
        int ox = (w - 7 * cell) / 2;
        int oy = (h - 6 * cell) / 2;
        for (int[] r : CELLS) {
            c.drawRect(
                    ox + r[0] * cell,
                    oy + r[1] * cell,
                    ox + (r[0] + r[2]) * cell,
                    oy + (r[1] + r[3]) * cell,
                    fill);
        }
    }
}
