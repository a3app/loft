/*
 * Android client, phone build: the older per-route screen, a plain list of
 * CIDRs (address prefixes such as 10.0.0.0/8) each sent through the tunnel,
 * around it, or dropped. Kept for existing settings in Prefs.routes;
 * SplitStore migrates those routes into its lists the first time it loads,
 * and ListsActivity is the current editor. The activity is still declared in
 * the manifest, but no screen in the app starts it any more.
 */
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
    /** Stored values for the three route choices, in the same order as the spinner entries. */
    private static final String[] VIA = {"through", "bypass", "drop"};
    /** Working copy of the saved routes; every change is written back with persist(). */
    private List<Prefs.Route> list;
    /** Adapter that shows labels() in the ListView; refilled by persist(). */
    private ArrayAdapter<String> adapter;

    /**
     * Builds the whole screen in code: title, CIDR input, through/bypass/drop picker, Add button
     * and the route list. Input that is not a valid prefix is ignored.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        list = new ArrayList<>(Prefs.routes(this));
        // 1. Page container and title. Colours are hard-coded ink/snow values from the
        //    app palette because this old screen has no XML layout.
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
        // 2. The route list; long-press on a row deletes that route and saves at once.
        ListView lv = new ListView(this);
        adapter = new ArrayAdapter<>(this, android.R.layout.simple_list_item_1, labels());
        lv.setAdapter(adapter);
        lv.setOnItemLongClickListener((a, v, pos, id) -> {
            list.remove(pos);
            persist();
            return true;
        });
        // 3. Input row: prefix text, through/bypass/drop picker, Add button.
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
            // Only a valid numeric prefix is accepted (Cidr.parse does no DNS lookups).
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
        // The list takes all remaining height (weight 1).
        root.addView(lv, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1));
        setContentView(root);
    }

    /** One display line per route: "prefix · choice". */
    private List<String> labels() {
        List<String> out = new ArrayList<>();
        for (Prefs.Route r : list) {
            out.add(r.cidr + " · " + r.via);
        }
        return out;
    }

    /**
     * Saves the list to Prefs (as JSON, written in the background with apply()) and
     * refreshes the visible rows.
     */
    private void persist() {
        Prefs.setRoutes(this, list);
        adapter.clear();
        adapter.addAll(labels());
        adapter.notifyDataSetChanged();
    }
}
