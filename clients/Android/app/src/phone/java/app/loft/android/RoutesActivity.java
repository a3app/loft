package app.loft.android;

import android.os.Bundle;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.Spinner;
import android.widget.TextView;



import java.util.ArrayList;
import java.util.List;

/**
 * Older per-route screen: a list of CIDRs, each sent through the tunnel,
 * around it, or dropped. Stores into Prefs.routes; SplitStore moves those
 * routes into its lists the first time it loads. Built in code, no layout.
 * Long-press removes a route.
 */
public final class RoutesActivity extends LoftActivity {
    private static final String[] VIA = {"through", "bypass", "drop"};
    private List<Prefs.Route> list;
    private ArrayAdapter<String> adapter;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        list = new ArrayList<>(Prefs.routes(this));
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF1A1714);
        int p = (int) (16 * getResources().getDisplayMetrics().density);
        root.setPadding(p, p, p, p);
        TextView t = new TextView(this);
        t.setText(R.string.routes);
        t.setTextColor(0xFFF3EDE4);
        t.setTextSize(20);
        root.addView(t);
        ListView lv = new ListView(this);
        adapter = new ArrayAdapter<>(this, android.R.layout.simple_list_item_1, labels());
        lv.setAdapter(adapter);
        lv.setOnItemLongClickListener((a, v, pos, id) -> {
            list.remove(pos);
            persist();
            return true;
        });
        EditText cidr = new EditText(this);
        cidr.setHint(R.string.route_add);
        cidr.setTextColor(0xFFF3EDE4);
        cidr.setHintTextColor(0xFF8A7E72);
        Spinner via = new Spinner(this);
        via.setAdapter(new ArrayAdapter<>(this, android.R.layout.simple_spinner_dropdown_item,
                new String[]{getString(R.string.via_through), getString(R.string.via_bypass), getString(R.string.via_drop)}));
        Button add = new Button(this);
        add.setText(R.string.add);
        add.setOnClickListener(v -> {
            String s = cidr.getText().toString().trim();
            if (Cidr.parse(s) == null) {
                return;
            }
            Prefs.Route r = new Prefs.Route();
            r.cidr = s;
            r.via = VIA[via.getSelectedItemPosition()];
            list.add(r);
            persist();
            cidr.setText("");
        });
        root.addView(cidr);
        root.addView(via);
        root.addView(add);
        root.addView(lv, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1));
        setContentView(root);
    }

    private List<String> labels() {
        List<String> out = new ArrayList<>();
        for (Prefs.Route r : list) {
            out.add(r.cidr + " · " + r.via);
        }
        return out;
    }

    private void persist() {
        Prefs.setRoutes(this, list);
        adapter.clear();
        adapter.addAll(labels());
        adapter.notifyDataSetChanged();
    }
}
