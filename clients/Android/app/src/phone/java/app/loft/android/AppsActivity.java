/*
 * Android client, phone build: the per-app routing screen (Settings, Route,
 * Apps). Lets the user pick which installed apps skip the tunnel or are the
 * only ones using it. The choice is stored in Prefs and applied by
 * LoftVpnService when it builds the TUN (the virtual network interface the
 * VPN reads packets from), through VpnService.Builder's
 * addDisallowedApplication / addAllowedApplication. While the VPN runs, the
 * service watches these Prefs keys and rebuilds the TUN after each change.
 */
package app.loft.android;

import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.graphics.drawable.Drawable;
import android.os.Bundle;
import android.text.TextUtils;
import android.util.LruCache;
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
import java.util.concurrent.ConcurrentHashMap;

/**
 * Settings → Route → Apps. Three modes: all apps follow split rules (split
 * tunneling: the zone lists decide per address what goes through the tunnel);
 * except — selected packages skip the tunnel; only — only selected
 * packages use it. Except and only keep separate package sets, so switching
 * modes does not lose the other mode's picks. The whole screen is built in
 * code inside the "body" container of activity_apps.xml.
 */
public final class AppsActivity extends LoftActivity {
    /** Current mode: "all", "except" or "only". */
    private String mode;
    /** Whether system apps are listed. Not saved; system apps already picked are always shown. */
    private boolean sys;
    /** Package names picked for "except" mode (these apps bypass the tunnel). */
    private final Set<String> except = new HashSet<>();
    /** Package names picked for "only" mode (only these apps use the tunnel). */
    private final Set<String> only = new HashSet<>();
    /**
     * Every launchable app (except loft itself), read from the system once per visit to
     * this screen by load() in the background. Null until that read is done. Searching,
     * the system-apps switch and mode changes only filter this list in memory.
     */
    private List<Row> all;
    /** True while load() is running, so a second fill() does not start another read. */
    private boolean loading;
    /** Apps currently shown in the list, after search and system-app filtering; rebuilt by fill(). */
    private final List<Row> rows = new ArrayList<>();
    /**
     * Icons already loaded, by package name. Icons are loaded lazily, only for rows that
     * are on screen, and kept here so scrolling back does not load them again. Bounded
     * (an icon can hold a sizeable bitmap) so a long scroll through hundreds of apps does
     * not keep every icon in memory; the oldest unused ones are dropped first.
     */
    private final LruCache<String, Drawable> icons = new LruCache<>(80);
    /** Packages whose icon is being loaded right now, so one package is never loaded twice at once. */
    private final Set<String> iconBusy = new HashSet<>();
    /** Packages whose icon failed to load; not retried, the row just shows no icon (as before). */
    private final Set<String> iconFailed = new HashSet<>();
    /**
     * Packages some on-screen row is waiting an icon for. Read by the background icon job
     * (hence a concurrent set) so it can skip apps whose row scrolled away before the job ran.
     */
    private final Set<String> iconWanted = ConcurrentHashMap.newKeySet();
    /** Every row view built by makeRow (only as many as fit on screen, plus a few). */
    private final List<Holder> holders = new ArrayList<>();
    /**
     * Search debounce: typing schedules one fill() 120 ms after the last keystroke, so
     * fast typing filters once instead of once per letter.
     */
    private final Runnable refill = this::fill;
    /** Adapter of the app list; null in "all" mode, where the list is not built. */
    private BaseAdapter adapter;
    /** Grey hint under the mode switch: a tip in "all" mode, a caveat about bypassing apps otherwise. */
    private TextView hint;
    /** "N selected" counter above the list; null in "all" mode. */
    private TextView cnt;
    /** Search field that filters the list by app name or package; null in "all" mode. */
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

    /** Cancels a pending search refill so it does not run against a closed screen. */
    @Override
    protected void onDestroy() {
        Ui.MAIN.removeCallbacks(refill);
        super.onDestroy();
    }

    /**
     * Saves the mode and both package sets to Prefs right away, so the next tunnel start uses them.
     * Writes use apply() (asynchronous disk write). Each written key that changed also makes a
     * running LoftVpnService rebuild its TUN, because the service listens to these Prefs keys.
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
        // The old list's row views are gone with the old screen; forget them so icon
        // results are not delivered to (and do not keep alive) views nobody sees.
        holders.clear();
        iconWanted.clear();
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

        // "All" needs no app list at all; except/only build the picker below.
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

            // Plain BaseAdapter over rows; getView reuses old row views (conv) so scrolling
            // only rebinds text, icon and switch instead of building new views.
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
            // Every edit of the search text schedules fill() (an in-memory filter) 120 ms later;
            // a newer keystroke pushes it back, so a burst of typing filters once.
            search.addTextChangedListener(new android.text.TextWatcher() {
                @Override
                public void beforeTextChanged(CharSequence s, int a, int b, int c) {
                }

                @Override
                public void onTextChanged(CharSequence s, int a, int b, int c) {
                }

                @Override
                public void afterTextChanged(android.text.Editable s) {
                    Ui.MAIN.removeCallbacks(refill);
                    Ui.MAIN.postDelayed(refill, 120);
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
     * Runs on the main thread but only filters the in-memory list {@link #all}, which is cheap.
     * The first call starts load() in the background and shows an empty list until the apps
     * arrive; load() then calls fill() again. Icons are not touched here (see bindRow).
     */
    private void fill() {
        Ui.MAIN.removeCallbacks(refill);
        Set<String> picked = sel();
        if (cnt != null) {
            Ui.text(cnt, getString(R.string.apps_cnt, picked.size()));
        }
        if (all == null) {
            load();
            return;
        }
        rows.clear();
        String q = search == null ? "" : search.getText().toString().trim().toLowerCase();
        for (Row r : all) {
            // Hide preinstalled (system) apps unless the switch is on, but never hide one that
            // is already picked, so the user can always see and undo a choice.
            if (r.system && !sys && !picked.contains(r.pkg)) {
                continue;
            }
            // Search matches the visible name or the package id, case-insensitively.
            if (!q.isEmpty() && !r.lname.contains(q) && !r.lpkg.contains(q)) {
                continue;
            }
            rows.add(r);
        }
        // Picked apps first, then alphabetical by name.
        Collections.sort(rows, (a, b) -> {
            boolean ap = picked.contains(a.pkg);
            boolean bp = picked.contains(b.pkg);
            if (ap != bp) {
                return ap ? -1 : 1;
            }
            return a.name.compareToIgnoreCase(b.name);
        });
        if (adapter != null) {
            adapter.notifyDataSetChanged();
        }
    }

    /**
     * Reads the installed launchable apps once, in the background (Ui.bg), then calls fill().
     * Every step here asks PackageManager, which is a call into the system server, and
     * reading an app's label loads that app's resources; with the many apps HyperOS ships
     * this took seconds and froze the screen when it ran on the main thread on every
     * keystroke. Only package, name and the system flag are read here; icons come later,
     * per visible row.
     */
    private void load() {
        if (loading) {
            return;
        }
        loading = true;
        PackageManager pm = getPackageManager();
        String self = getPackageName();
        Ui.bg(this, () -> {
            // 1. Ask for every activity that shows in the launcher (MAIN + LAUNCHER). This lists
            //    apps the user can open, which is what they expect to choose from.
            android.content.Intent launch = new android.content.Intent(android.content.Intent.ACTION_MAIN)
                    .addCategory(android.content.Intent.CATEGORY_LAUNCHER);
            List<ResolveInfo> found = pm.queryIntentActivities(launch, 0);
            // 2. One row per package: an app with several launcher icons appears once; loft itself
            //    is skipped (it cannot route around its own tunnel).
            List<Row> out = new ArrayList<>();
            Set<String> seen = new HashSet<>();
            for (ResolveInfo ri : found) {
                if (ri.activityInfo == null) {
                    continue;
                }
                String pkg = ri.activityInfo.packageName;
                if (!seen.add(pkg) || self.equals(pkg)) {
                    continue;
                }
                // The query result already carries the app's info; ask the system only when
                // it does not, which saves one system call per app.
                ApplicationInfo ai = ri.activityInfo.applicationInfo;
                if (ai == null) {
                    try {
                        ai = pm.getApplicationInfo(pkg, 0);
                    } catch (PackageManager.NameNotFoundException e) {
                        continue;
                    }
                }
                Row r = new Row();
                r.pkg = pkg;
                r.info = ai;
                r.name = String.valueOf(pm.getApplicationLabel(ai));
                r.system = (ai.flags & ApplicationInfo.FLAG_SYSTEM) != 0;
                // Lower-case copies made once here, so each search keystroke compares directly.
                r.lname = r.name.toLowerCase();
                r.lpkg = pkg.toLowerCase();
                out.add(r);
            }
            return out;
        }, (list, err) -> {
            loading = false;
            // A failed read leaves the list empty, which is what an empty system answer showed before.
            all = list != null ? list : new ArrayList<>();
            fill();
        });
    }

    /**
     * Shows the icon for {@code r} in a row: from the cache when it is there, otherwise the
     * image is cleared and the icon is loaded in the background. A row view is reused for
     * other apps while scrolling, so when the icon arrives it is put only into rows that
     * still show that app ({@link Holder#want}); a recycled row never gets the wrong icon.
     */
    private void showIcon(Holder h, Row r) {
        if (h.want != null) {
            iconWanted.remove(h.want);
        }
        h.want = r.pkg;
        Drawable d = icons.get(r.pkg);
        h.ic.setImageDrawable(d);
        if (d != null || iconFailed.contains(r.pkg)) {
            return;
        }
        iconWanted.add(r.pkg);
        loadIcon(r.pkg, r.info);
    }

    /**
     * Loads one app's icon in the background, caches it and puts it into every row that
     * still wants it. Does nothing when the same icon is already being loaded.
     */
    private void loadIcon(String pkg, ApplicationInfo ai) {
        if (!iconBusy.add(pkg)) {
            return; // already on its way; the result goes to every row that wants it
        }
        PackageManager pm = getPackageManager();
        Ui.bg(this, () -> {
            // The row may have scrolled away while this job waited its turn; then skip the
            // (relatively slow) icon load, it will be asked for again if the app comes back.
            if (!iconWanted.contains(pkg)) {
                return null;
            }
            return pm.getApplicationIcon(ai);
        }, (icon, err) -> {
            iconBusy.remove(pkg);
            if (err != null) {
                // A missing or broken icon just leaves the image empty, as before.
                iconFailed.add(pkg);
                return;
            }
            if (icon == null) {
                // Skipped because no row wanted it when the job ran. A row may have asked
                // again since then (it found the load "busy" and waited), so start over
                // if anyone wants it now.
                if (iconWanted.contains(pkg)) {
                    loadIcon(pkg, ai);
                }
                return;
            }
            icons.put(pkg, icon);
            for (Holder o : holders) {
                if (pkg.equals(o.want)) {
                    o.ic.setImageDrawable(icon);
                }
            }
        });
    }

    /**
     * Creates one empty list row (icon, name, package, switch). The row is built in code
     * without view ids, so its parts are kept in a {@link Holder} stored as the row's tag,
     * which bindRow reads back.
     */
    private View makeRow() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(0, dp(6), 0, dp(6));
        ImageView ic = new ImageView(this);
        LinearLayout.LayoutParams ilp = new LinearLayout.LayoutParams(dp(32), dp(32));
        ilp.rightMargin = dp(10);
        row.addView(ic, ilp);
        LinearLayout grow = new LinearLayout(this);
        grow.setOrientation(LinearLayout.VERTICAL);
        TextView n = new TextView(this);
        n.setTextColor(getColor(R.color.snow));
        n.setTextSize(13.5f);
        n.setMaxLines(1);
        n.setEllipsize(TextUtils.TruncateAt.END);
        TextView m = new TextView(this);
        m.setTextColor(getColor(R.color.mute));
        m.setTextSize(10.5f);
        m.setTypeface(android.graphics.Typeface.MONOSPACE);
        m.setMaxLines(1);
        m.setEllipsize(TextUtils.TruncateAt.END);
        grow.addView(n);
        grow.addView(m);
        row.addView(grow, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        ToggleView sw = new ToggleView(this);
        row.addView(sw, new LinearLayout.LayoutParams(dp(42), dp(26)));
        Holder h = new Holder();
        h.ic = ic;
        h.n = n;
        h.m = m;
        h.sw = sw;
        row.setTag(h);
        holders.add(h);
        return row;
    }

    /**
     * Fills a row for one app and makes its switch (or a tap on the row) add or remove the
     * package from the current set, saving immediately.
     * The listener is cleared before setChecked: a recycled row still carries the previous
     * app's listener, which would otherwise fire and change the wrong package.
     */
    private void bindRow(View row, Row r) {
        Holder h = (Holder) row.getTag();
        showIcon(h, r);
        Ui.text(h.n, r.name);
        Ui.text(h.m, r.pkg);
        ToggleView sw = h.sw;
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
                Ui.text(cnt, getString(R.string.apps_cnt, sel().size()));
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
        /** Package name, e.g. com.example.app; this is what Prefs stores. */
        String pkg = "";
        /** Name shown in the launcher. */
        String name = "";
        /** True for preinstalled system apps (FLAG_SYSTEM). */
        boolean system;
        /** The app's info from the system, kept to load the icon later without asking again. */
        ApplicationInfo info;
        /** Lower-case name and package, for the search filter. */
        String lname = "";
        String lpkg = "";
    }

    /** The parts of one row view, so binding does not have to search the row for them. */
    private static final class Holder {
        ImageView ic;
        TextView n;
        TextView m;
        ToggleView sw;
        /** Package whose icon this row should show now; null before the first bind. */
        String want;
    }
}
