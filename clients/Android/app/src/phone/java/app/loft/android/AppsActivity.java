/*
 * Android client, phone build: the per-app routing screen (Settings, Route,
 * Apps). Lets the user pick which installed apps skip the tunnel or are the
 * only ones using it. The choice is stored in Prefs and applied by
 * LoftVpnService when it builds the TUN.
 */
package app.loft.android;

import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.graphics.drawable.Drawable;
import android.os.Bundle;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.EditText;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.TextView;

import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/**
 * Settings → Route → Apps. Three modes: all apps follow split rules;
 * except — selected packages skip the tunnel; only — only selected
 * packages use it. Except and only keep separate package sets.
 */
public final class AppsActivity extends LoftActivity {
    /** Current mode: "all", "except" or "only". */
    private String mode;
    /** Whether system apps are listed. Not saved; system apps already picked are always shown. */
    private boolean sys;
    private final Set<String> except = new HashSet<>();
    private final Set<String> only = new HashSet<>();
    private final List<Row> rows = new ArrayList<>();
    private BaseAdapter adapter;
    private TextView hint;
    private TextView cnt;
    private EditText search;
    /** Container for search, system-apps switch, counter and app list; hidden in "all" mode. */
    private LinearLayout extra;

    /** Loads the saved mode and both package sets, then draws the screen. */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_apps);
        findViewById(R.id.btn_back).setOnClickListener(v -> finish());
        mode = Prefs.appMode(this);
        except.addAll(Prefs.appSet(this, "except"));
        only.addAll(Prefs.appSet(this, "only"));
        paint();
    }

    /**
     * Saves the mode and both package sets to Prefs right away, so the next tunnel start uses them.
     */
    private void persist() {
        Prefs.p(this).edit().putString("app_mode", mode).apply();
        Prefs.setAppSet(this, "except", except);
        Prefs.setAppSet(this, "only", only);
    }

    /** The package set the current mode edits ("only" set, else the "except" set). */
    private Set<String> sel() {
        return "only".equals(mode) ? only : except;
    }

    /**
     * Rebuilds the whole screen in code for the current mode: description, mode switch and hint,
     * and, for except/only, the search field, system-apps switch, counter and app list.
     * Called again whenever the mode changes.
     */
    private void paint() {
        ViewGroup body = findViewById(R.id.body);
        body.removeAllViews();
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        int p = dp(16);
        root.setPadding(p, p / 2, p, p);

        // 1. Mode description and the All / Except / Only switch.
        TextView sub = new TextView(this);
        int d = "only".equals(mode) ? R.string.apps_d_only
                : "except".equals(mode) ? R.string.apps_d_except : R.string.apps_d_all;
        sub.setText(d);
        sub.setTextColor(getColor(R.color.mute));
        sub.setTextSize(12.5f);
        LinearLayout.LayoutParams slp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        slp.bottomMargin = dp(12);
        sub.setLayoutParams(slp);
        root.addView(sub);

        LinearLayout seg = new LinearLayout(this);
        seg.setOrientation(LinearLayout.HORIZONTAL);
        String[] labels = {
                getString(R.string.apps_seg_all),
                getString(R.string.apps_seg_except),
                getString(R.string.apps_seg_only)
        };
        for (String t : labels) {
            android.widget.Button b = (android.widget.Button)
                    getLayoutInflater().inflate(R.layout.btn_seg, seg, false);
            b.setText(t);
            seg.addView(b);
        }
        int idx = "only".equals(mode) ? 2 : "except".equals(mode) ? 1 : 0;
        Seg.bind(seg, idx, i -> {
            mode = i == 2 ? "only" : i == 1 ? "except" : "all";
            persist();
            paint();
        });
        LinearLayout.LayoutParams segLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        segLp.bottomMargin = dp(10);
        root.addView(seg, segLp);

        // 2. Hint line, then the area that holds the app picker.
        hint = new TextView(this);
        hint.setTextColor(getColor(R.color.mute));
        hint.setTextSize(12);
        hint.setLineSpacing(0, 1.45f);
        root.addView(hint);

        extra = new LinearLayout(this);
        extra.setOrientation(LinearLayout.VERTICAL);
        extra.setLayoutParams(new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        root.addView(extra);

        if ("all".equals(mode)) {
            hint.setText(R.string.apps_all_h);
            extra.setVisibility(View.GONE);
        } else {
            hint.setText(R.string.apps_lock);
            extra.setVisibility(View.VISIBLE);
            // 3. Except/only: search field, system-apps switch, counter and the app list.
            search = new EditText(this);
            search.setHint(R.string.apps_search);
            search.setTextColor(getColor(R.color.snow));
            search.setHintTextColor(getColor(R.color.mute));
            search.setBackgroundResource(R.drawable.bg_field);
            search.setPadding(dp(12), dp(10), dp(12), dp(10));
            search.setTextSize(14);
            search.setSingleLine(true);
            LinearLayout.LayoutParams flp = new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            flp.topMargin = dp(10);
            search.setLayoutParams(flp);
            extra.addView(search);

            LinearLayout sysRow = new LinearLayout(this);
            sysRow.setOrientation(LinearLayout.HORIZONTAL);
            sysRow.setGravity(Gravity.CENTER_VERTICAL);
            sysRow.setPadding(0, dp(8), 0, dp(8));
            TextView sk = new TextView(this);
            sk.setText(R.string.apps_sys);
            sk.setTextColor(getColor(R.color.snow));
            sk.setTextSize(13);
            sysRow.addView(sk, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
            ToggleView sw = new ToggleView(this);
            sw.setChecked(sys);
            sw.setOnCheckedChangeListener((v, on) -> {
                sys = on;
                fill();
            });
            sysRow.addView(sw, new LinearLayout.LayoutParams(dp(42), dp(26)));
            extra.addView(sysRow);

            cnt = new TextView(this);
            cnt.setTextColor(getColor(R.color.mute));
            cnt.setTextSize(12);
            extra.addView(cnt);

            adapter = new BaseAdapter() {
                @Override
                public int getCount() {
                    return rows.size();
                }

                @Override
                public Object getItem(int i) {
                    return rows.get(i);
                }

                @Override
                public long getItemId(int i) {
                    return i;
                }

                @Override
                public View getView(int i, View conv, ViewGroup parent) {
                    if (conv == null) {
                        conv = makeRow();
                    }
                    bindRow(conv, rows.get(i));
                    return conv;
                }
            };
            ListView lv = new ListView(this);
            lv.setAdapter(adapter);
            lv.setDivider(new android.graphics.drawable.ColorDrawable(0));
            lv.setDividerHeight(dp(4));
            lv.setSelector(new android.graphics.drawable.ColorDrawable(0));
            extra.addView(lv, new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
            search.addTextChangedListener(new android.text.TextWatcher() {
                @Override
                public void beforeTextChanged(CharSequence s, int a, int b, int c) {
                }

                @Override
                public void onTextChanged(CharSequence s, int a, int b, int c) {
                }

                @Override
                public void afterTextChanged(android.text.Editable s) {
                    fill();
                }
            });
            fill();
        }
        body.addView(root, new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    }

    /**
     * Refills the app list: every launchable app except loft itself, filtered by the search text,
     * with system apps hidden unless the switch is on or they are already picked. Picked apps
     * come first, then by name.
     */
    private void fill() {
        rows.clear();
        String q = search == null ? "" : search.getText().toString().trim().toLowerCase();
        Set<String> picked = sel();
        PackageManager pm = getPackageManager();
        android.content.Intent launch = new android.content.Intent(android.content.Intent.ACTION_MAIN)
                .addCategory(android.content.Intent.CATEGORY_LAUNCHER);
        List<ResolveInfo> found = pm.queryIntentActivities(launch, 0);
        Set<String> seen = new HashSet<>();
        for (ResolveInfo ri : found) {
            if (ri.activityInfo == null) {
                continue;
            }
            String pkg = ri.activityInfo.packageName;
            if (!seen.add(pkg) || getPackageName().equals(pkg)) {
                continue;
            }
            ApplicationInfo ai;
            try {
                ai = pm.getApplicationInfo(pkg, 0);
            } catch (PackageManager.NameNotFoundException e) {
                continue;
            }
            boolean system = (ai.flags & ApplicationInfo.FLAG_SYSTEM) != 0;
            if (system && !sys && !picked.contains(pkg)) {
                continue;
            }
            String name = String.valueOf(pm.getApplicationLabel(ai));
            if (!q.isEmpty() && !name.toLowerCase().contains(q) && !pkg.toLowerCase().contains(q)) {
                continue;
            }
            Row r = new Row();
            r.pkg = pkg;
            r.name = name;
            r.system = system;
            try {
                r.icon = pm.getApplicationIcon(ai);
            } catch (Exception ignored) {
            }
            rows.add(r);
        }
        Collections.sort(rows, (a, b) -> {
            boolean ap = picked.contains(a.pkg);
            boolean bp = picked.contains(b.pkg);
            if (ap != bp) {
                return ap ? -1 : 1;
            }
            return a.name.compareToIgnoreCase(b.name);
        });
        if (cnt != null) {
            cnt.setText(getString(R.string.apps_cnt, picked.size()));
        }
        if (adapter != null) {
            adapter.notifyDataSetChanged();
        }
    }

    /** Creates one empty list row (icon, name, package, switch); views are found later by tag. */
    private View makeRow() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(0, dp(6), 0, dp(6));
        ImageView ic = new ImageView(this);
        ic.setTag("ic");
        LinearLayout.LayoutParams ilp = new LinearLayout.LayoutParams(dp(32), dp(32));
        ilp.rightMargin = dp(10);
        row.addView(ic, ilp);
        LinearLayout grow = new LinearLayout(this);
        grow.setOrientation(LinearLayout.VERTICAL);
        TextView n = new TextView(this);
        n.setTag("n");
        n.setTextColor(getColor(R.color.snow));
        n.setTextSize(13.5f);
        n.setMaxLines(1);
        n.setEllipsize(TextUtils.TruncateAt.END);
        TextView m = new TextView(this);
        m.setTag("m");
        m.setTextColor(getColor(R.color.mute));
        m.setTextSize(10.5f);
        m.setTypeface(android.graphics.Typeface.MONOSPACE);
        m.setMaxLines(1);
        m.setEllipsize(TextUtils.TruncateAt.END);
        grow.addView(n);
        grow.addView(m);
        row.addView(grow, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        ToggleView sw = new ToggleView(this);
        sw.setTag("sw");
        row.addView(sw, new LinearLayout.LayoutParams(dp(42), dp(26)));
        return row;
    }

    /**
     * Fills a row for one app and makes its switch (or a tap on the row) add or remove the
     * package from the current set, saving immediately.
     */
    private void bindRow(View row, Row r) {
        ImageView ic = row.findViewWithTag("ic");
        if (r.icon != null) {
            ic.setImageDrawable(r.icon);
        } else {
            ic.setImageDrawable(null);
        }
        ((TextView) row.findViewWithTag("n")).setText(r.name);
        ((TextView) row.findViewWithTag("m")).setText(r.pkg);
        ToggleView sw = row.findViewWithTag("sw");
        sw.setOnCheckedChangeListener(null);
        sw.setChecked(sel().contains(r.pkg));
        sw.setOnCheckedChangeListener((v, on) -> {
            if (on) {
                sel().add(r.pkg);
            } else {
                sel().remove(r.pkg);
            }
            persist();
            if (cnt != null) {
                cnt.setText(getString(R.string.apps_cnt, sel().size()));
            }
        });
        row.setOnClickListener(v -> sw.toggle());
    }

    /** Converts density-independent pixels to real pixels for this screen. */
    private int dp(int v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }

    /** One installed app as shown in the list. */
    private static final class Row {
        String pkg = "";
        String name = "";
        boolean system;
        Drawable icon;
    }
}
