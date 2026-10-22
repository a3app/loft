package app.loft.android;

import android.os.Bundle;
import android.widget.LinearLayout;
import android.widget.TextView;

import androidx.appcompat.app.AppCompatActivity;

public final class AboutActivity extends AppCompatActivity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF1A1714);
        int p = (int) (20 * getResources().getDisplayMetrics().density);
        root.setPadding(p, p * 2, p, p);
        TextView t = tv(28, 0xFFF3EDE4, true);
        t.setText("loft");
        root.addView(t);
        TextView v = tv(14, 0xFF8A7E72, false);
        v.setText("1.0.24");
        root.addView(v);
        TextView proto = tv(14, 0xFFB7A99A, false);
        proto.setText(R.string.about_proto);
        root.addView(proto);
        TextView fronts = tv(14, 0xFFB7A99A, false);
        fronts.setText(R.string.about_fronts);
        root.addView(fronts);
        TextView blurb = tv(16, 0xFFF3EDE4, false);
        blurb.setText(R.string.about_blurb);
        blurb.setPadding(0, p, 0, p);
        root.addView(blurb);
        TextView made = tv(13, 0xFF8A7E72, false);
        made.setText(R.string.about_made);
        root.addView(made);
        setContentView(root);
    }

    private TextView tv(int sp, int color, boolean bold) {
        TextView t = new TextView(this);
        t.setTextSize(sp);
        t.setTextColor(color);
        if (bold) {
            t.setTypeface(t.getTypeface(), android.graphics.Typeface.BOLD);
        }
        return t;
    }
}
