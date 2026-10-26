package app.loft.android;

import android.app.AlertDialog;
import android.content.Intent;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import com.google.android.material.button.MaterialButton;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;

/**
 * Editor for the split-routing lists (SplitStore): which address ranges go
 * through the tunnel and which go direct. One activity with several pages
 * built in code: the list of a zone, zones, one zone, one service, the
 * repository settings and the catalog of ready-made lists. Back returns to
 * the list page first. Saving rebuilds the live TUN when a session is up.
 */
public final class ListsActivity extends LoftActivity {
    private static final int REQ_IN = 8;
    private SplitStore store;
    private String zoneId = SplitStore.GLOBAL;
    private String page = "list";
    // Id of the zone or service the "zone" / "svc" page is editing.
    private String editing;
    // Zone whose list URL the catalog writes; page to return to from catalog.
    private String catalogZone = SplitStore.GLOBAL;
    private String catalogBack = "repo";
    private final Handler main = new Handler(Looper.getMainLooper());

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_lists);
        store = SplitStore.load(this);
        ToggleView split = findViewById(R.id.sw_split);
        split.setChecked(Prefs.split(this));
        split.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("split", on).apply());
        findViewById(R.id.btn_back).setOnClickListener(v -> onBackPressed());
        View more = findViewById(R.id.btn_more);
        if (more != null) {
            more.setVisibility(View.GONE);
        }
        show("list");
    }

    @Override
    public void onBackPressed() {
        if ("catalog".equals(page)) {
            show(catalogBack);
            return;
        }
        if (!"list".equals(page)) {
            show("list");
            return;
        }
        super.onBackPressed();
    }

    private void persist() {
        store.save(this);
    }

    /** Switches to a page by name and rebuilds it from the store. */
    private void show(String p) {
        page = p;
        TextView title = findViewById(R.id.title);
        findViewById(R.id.sw_split).setVisibility("list".equals(p) ? View.VISIBLE : View.GONE);
        ViewGroup body = findViewById(R.id.body);
        body.removeAllViews();
        View page;
        switch (p) {
            case "zones":
                title.setText(R.string.zones);
                page = zonesPage();
                break;
            case "zone":
                title.setText(R.string.zone);
                page = zonePage();
                break;
            case "svc":
                title.setText(R.string.service);
                page = svcPage();
                break;
            case "repo":
                title.setText(R.string.repository);
                page = repoPage();
                break;
            case "catalog":
                title.setText(R.string.catalog);
                page = catalogPage();
                break;
            default:
                title.setText(R.string.lists);
                page = listPage();
                break;
        }
        body.addView(page, new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    }

    /**
     * Main page: zone chips, the zone's policy, a searchable list of its
     * services, add (a service or an ignore entry), and import / export as a
     * .bat file and the repository page.
     */
    private View listPage() {
        LinearLayout root = col();
        LinearLayout zhead = row();
        zhead.addView(label(getString(R.string.zone)),
                new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        zhead.addView(ghostSm(getString(R.string.zones), v -> show("zones")));
        root.addView(zhead);
        LinearLayout chips = new LinearLayout(this);
        chips.setOrientation(LinearLayout.HORIZONTAL);
        for (SplitStore.Zone z : store.zones) {
            Button b = pill(z.name + " " + store.count(z.id));
            b.setSelected(z.id.equals(zoneId));
            b.setOnClickListener(v -> {
                zoneId = z.id;
                show("list");
            });
            chips.addView(b);
        }
        HorizontalScrollView hs = new HorizontalScrollView(this);
        hs.setHorizontalScrollBarEnabled(false);
        hs.addView(chips);
        LinearLayout.LayoutParams hsLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        hsLp.bottomMargin = dp(8);
        root.addView(hs, hsLp);
        SplitStore.Zone z = store.zone(zoneId);
        root.addView(policyLine(z != null && z.through
                ? getString(R.string.policy_through, z.name)
                : getString(R.string.policy_around, z == null ? zoneId : z.name)));
        EditText search = field("");
        search.setHint(getString(R.string.search));
        search.setInputType(android.text.InputType.TYPE_CLASS_TEXT);
        root.addView(search);
        final List<SplitStore.Svc> rows = new ArrayList<>();
        final BaseAdapter adapter = new BaseAdapter() {
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
                    conv = makeSvcRow();
                }
                bindSvcRow(conv, rows.get(i));
                return conv;
            }
        };
        TextView empty = hint(getString(R.string.empty_zone));
        empty.setVisibility(View.GONE);
        root.addView(empty);
        ListView lv = new ListView(this);
        lv.setAdapter(adapter);
        lv.setDivider(new android.graphics.drawable.ColorDrawable(0));
        lv.setDividerHeight(dp(6));
        lv.setSelector(new android.graphics.drawable.ColorDrawable(0));
        lv.setNestedScrollingEnabled(true);
        Runnable paint = () -> {
            rows.clear();
            rows.addAll(store.in(zoneId, search.getText().toString()));
            empty.setVisibility(rows.isEmpty() ? View.VISIBLE : View.GONE);
            adapter.notifyDataSetChanged();
        };
        search.addTextChangedListener(new SimpleWatch(paint));
        paint.run();
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1);
        root.addView(lv, lp);
        Button add = ghostSm("+ " + getString(R.string.add), v -> new AlertDialog.Builder(this)
                .setItems(new CharSequence[]{getString(R.string.add_service), getString(R.string.add_ignore)},
                        (d, w) -> {
                            SplitStore.Svc s = new SplitStore.Svc();
                            s.zone = zoneId;
                            if (w == 1) {
                                s.name = "ignore";
                                s.src = "ignore";
                                s.ignore = true;
                            } else {
                                s.name = getString(R.string.service);
                            }
                            store.services.add(s);
                            persist();
                            editing = s.id;
                            show("svc");
                        })
                .show());
        LinearLayout.LayoutParams addLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        addLp.topMargin = dp(6);
        add.setLayoutParams(addLp);
        root.addView(add);
        LinearLayout foot = row();
        LinearLayout.LayoutParams footLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        footLp.topMargin = dp(10);
        foot.setLayoutParams(footLp);
        foot.addView(mini(getString(R.string.import_routes), v -> {
            Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
            i.addCategory(Intent.CATEGORY_OPENABLE);
            i.setType("*/*");
            startActivityForResult(i, REQ_IN);
        }), weight(0));
        foot.addView(mini(getString(R.string.export_routes), v -> {
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, Bat.emit(store, zoneId));
            i.putExtra(Intent.EXTRA_SUBJECT, zoneId + ".bat");
            startActivity(Intent.createChooser(i, getString(R.string.export_routes)));
        }), weight(6));
        foot.addView(mini(getString(R.string.repository), v -> show("repo")), weight(6));
        root.addView(foot);
        return root;
    }

    private View makeSvcRow() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setLayoutParams(new android.widget.AbsListView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        row.setBackgroundResource(R.drawable.bg_card);
        row.setPadding(dp(12), dp(10), dp(12), dp(10));
        LinearLayout grow = col(false);
        TextView n = new TextView(this);
        n.setTag("n");
        n.setTextColor(getColor(R.color.snow));
        n.setTextSize(13.5f);
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
        TextView src = meta("");
        src.setTag("src");
        row.addView(src);
        return row;
    }

    private void bindSvcRow(View row, SplitStore.Svc s) {
        ((TextView) row.findViewWithTag("n")).setText(s.name);
        ((TextView) row.findViewWithTag("m")).setText(SplitStore.preview(s.cidrs));
        ((TextView) row.findViewWithTag("src")).setText(s.ignore ? getString(R.string.ignore)
                : "repo".equals(s.src) ? getString(R.string.from_repo) : getString(R.string.mine));
        row.setOnClickListener(v -> {
            editing = s.id;
            show("svc");
        });
    }

    /**
     * Edit one service: name, prefixes (one per line or comma separated; bad
     * entries are dropped on save), its zone, and whether it is a route or an
     * ignore exclusion.
     */
    private View svcPage() {
        SplitStore.Svc s = store.svc(editing);
        if (s == null) {
            s = new SplitStore.Svc();
            s.zone = zoneId;
            store.services.add(s);
            editing = s.id;
        }
        SplitStore.Svc svc = s;
        LinearLayout root = col();
        root.addView(label(getString(R.string.name)));
        EditText name = field(svc.name);
        root.addView(name);
        root.addView(label(getString(R.string.networks)));
        EditText nets = field(SplitStore.join(svc.cidrs).replace(' ', '\n'));
        nets.setMinLines(3);
        nets.setGravity(Gravity.TOP);
        root.addView(nets);
        root.addView(label(getString(R.string.zone)));
        LinearLayout zseg = new LinearLayout(this);
        zseg.setOrientation(LinearLayout.HORIZONTAL);
        int zi = 0;
        for (int i = 0; i < store.zones.size(); i++) {
            SplitStore.Zone z = store.zones.get(i);
            Button b = chip(z.name);
            if (z.id.equals(svc.zone)) {
                zi = i;
            }
            zseg.addView(b);
        }
        Seg.bind(zseg, zi, i -> svc.zone = store.zones.get(i).id);
        HorizontalScrollView hs = new HorizontalScrollView(this);
        hs.addView(zseg);
        root.addView(hs);
        LinearLayout kind = new LinearLayout(this);
        kind.setOrientation(LinearLayout.HORIZONTAL);
        Button route = chip(getString(R.string.kind_route));
        Button ign = chip(getString(R.string.kind_ignore));
        kind.addView(route);
        kind.addView(ign);
        Seg.bind(kind, svc.ignore ? 1 : 0, i -> {
            svc.ignore = i == 1;
            svc.src = svc.ignore ? "ignore" : "me";
        });
        LinearLayout.LayoutParams kindLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        kindLp.bottomMargin = dp(8);
        kind.setLayoutParams(kindLp);
        root.addView(kind);
        Button save = go(getString(R.string.save), 14);
        save.setOnClickListener(v -> {
            svc.name = name.getText().toString().trim();
            svc.cidrs.clear();
            for (String line : nets.getText().toString().split("[\\s,]+")) {
                Cidr c = Cidr.parse(line);
                if (c != null) {
                    svc.cidrs.add(c.display());
                }
            }
            persist();
            show("list");
        });
        root.addView(save);
        if (!svc.ignore) {
            Button del = danger(getString(R.string.delete), 8);
            del.setOnClickListener(v -> {
                store.services.remove(svc);
                persist();
                show("list");
            });
            root.addView(del);
        }
        return scroll(root);
    }

    private View zonesPage() {
        LinearLayout root = col();
        for (SplitStore.Zone z : store.zones) {
            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.VERTICAL);
            row.setBackgroundResource(R.drawable.bg_card);
            row.setPadding(dp(12), dp(10), dp(12), dp(10));
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            lp.bottomMargin = dp(6);
            row.setLayoutParams(lp);
            TextView n = new TextView(this);
            n.setText(z.name);
            n.setTextColor(getColor(R.color.snow));
            row.addView(n);
            TextView m = new TextView(this);
            m.setText((z.through ? getString(R.string.through_loft) : getString(R.string.around_loft))
                    + " · " + store.count(z.id));
            m.setTextColor(getColor(R.color.mute));
            m.setTextSize(10.5f);
            m.setPadding(0, dp(2), 0, 0);
            row.addView(m);
            row.setOnClickListener(v -> {
                editing = z.id;
                show("zone");
            });
            root.addView(row);
        }
        root.addView(ghostSm("+ " + getString(R.string.new_zone), v -> {
            SplitStore.Zone z = new SplitStore.Zone();
            z.id = UUID.randomUUID().toString();
            z.name = getString(R.string.new_zone);
            z.through = true;
            store.zones.add(z);
            persist();
            editing = z.id;
            show("zone");
        }));
        return scroll(root);
    }

    private View zonePage() {
        SplitStore.Zone z = store.zone(editing);
        if (z == null) {
            show("zones");
            return col();
        }
        LinearLayout root = col();
        EditText name = field(z.name);
        name.setEnabled(!z.builtin);
        root.addView(label(getString(R.string.zone_name)));
        root.addView(name);
        LinearLayout dir = new LinearLayout(this);
        dir.setOrientation(LinearLayout.HORIZONTAL);
        dir.addView(chip(getString(R.string.through_loft)));
        dir.addView(chip(getString(R.string.around_loft)));
        Seg.bind(dir, z.through ? 0 : 1, i -> z.through = i == 0);
        LinearLayout.LayoutParams dirLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        dirLp.bottomMargin = dp(14);
        dir.setLayoutParams(dirLp);
        root.addView(dir);
        root.addView(label(getString(R.string.list_url)));
        EditText url = urlField(z.repo);
        root.addView(urlRow(url, v -> {
            z.repo = url.getText().toString().trim();
            persist();
            openCatalog(z.id, "zone");
        }));
        root.addView(autoRow(z));
        root.addView(hint(z.pulled == 0 ? getString(R.string.no_list) : getString(R.string.pulled)));
        Button save = go(getString(R.string.save), 14);
        save.setOnClickListener(v -> {
            if (!z.builtin) {
                z.name = name.getText().toString().trim();
            }
            z.repo = url.getText().toString().trim();
            persist();
            show("zones");
        });
        root.addView(save);
        if (!z.builtin) {
            Button del = danger(getString(R.string.delete_zone), 8);
            del.setOnClickListener(v -> {
                store.deleteZone(z.id);
                persist();
                show("zones");
            });
            root.addView(del);
        }
        return scroll(root);
    }

    /** Repository settings of the current zone: list URL, auto-update switch, and "pull" to download now. */
    private View repoPage() {
        SplitStore.Zone z = store.zone(zoneId);
        LinearLayout root = col();
        if (z == null) {
            return root;
        }
        root.addView(subline(z.name + " · " + getString(R.string.repo_d)));
        root.addView(label(getString(R.string.list_url)));
        EditText url = urlField(z.repo);
        url.addTextChangedListener(new SimpleWatch(() -> {
            z.repo = url.getText().toString().trim();
            persist();
        }));
        root.addView(urlRow(url, v -> {
            z.repo = url.getText().toString().trim();
            persist();
            openCatalog(z.id, "repo");
        }));
        root.addView(autoRow(z));
        root.addView(hint(z.pulled == 0 ? getString(R.string.no_list) : getString(R.string.pulled)));
        Button pull = go(getString(R.string.pull), 14);
        pull.setOnClickListener(v -> {
            z.repo = url.getText().toString().trim();
            persist();
            pull(z);
        });
        root.addView(pull);
        return scroll(root);
    }

    private void openCatalog(String zone, String back) {
        catalogZone = zone;
        catalogBack = back;
        show("catalog");
    }

    /** Ready-made lists from the loft-routes repository; picking one sets the zone's list URL. */
    private View catalogPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.catalog_d)));
        String lang = Prefs.applyLang(this);
        SplitStore.Zone cur = store.zone(catalogZone);
        String current = cur != null ? cur.repo : "";
        for (String[] row : SplitStore.CATALOG) {
            String id = row[0];
            String url = SplitStore.catalogUrl(id);
            LinearLayout card = new LinearLayout(this);
            card.setOrientation(LinearLayout.HORIZONTAL);
            card.setGravity(Gravity.CENTER_VERTICAL);
            card.setBackgroundResource(url.equals(current) ? R.drawable.bg_card_live : R.drawable.bg_card);
            card.setPadding(dp(12), dp(9), dp(12), dp(9));
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            lp.bottomMargin = dp(6);
            card.setLayoutParams(lp);
            LinearLayout grow = col(false);
            TextView n = new TextView(this);
            n.setText(SplitStore.catalogLabel(id, lang));
            n.setTextColor(getColor(R.color.snow));
            n.setTextSize(13.5f);
            n.getPaint().setFakeBoldText(true);
            TextView m = new TextView(this);
            m.setText(id + ".bat");
            m.setTextColor(getColor(R.color.mute));
            m.setTextSize(10.5f);
            m.setTypeface(android.graphics.Typeface.MONOSPACE);
            m.setPadding(0, dp(2), 0, 0);
            grow.addView(n);
            grow.addView(m);
            card.addView(grow, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
            card.addView(meta(getString(R.string.from_repo)));
            card.setOnClickListener(v -> {
                SplitStore.Zone z = store.zone(catalogZone);
                if (z != null) {
                    z.repo = url;
                    persist();
                }
                show(catalogBack);
            });
            root.addView(card);
        }
        return scroll(root);
    }

    /**
     * Downloads the zone's .bat list and replaces the zone's repository
     * services with it; user-added services stay.
     */
    private void pull(SplitStore.Zone z) {
        if (z.repo == null || z.repo.isEmpty()) {
            Toast.makeText(this, R.string.no_url, Toast.LENGTH_SHORT).show();
            return;
        }
        new Thread(() -> {
            try {
                store.pullZone(z);
                main.post(() -> {
                    persist();
                    Toast.makeText(this, R.string.pulled, Toast.LENGTH_SHORT).show();
                    show("list");
                });
            } catch (Exception e) {
                main.post(() -> Toast.makeText(this, R.string.pull_fail, Toast.LENGTH_SHORT).show());
            }
        }).start();
    }

    // Imported .bat file: goes to the zone named in its "REM zone:" header
    // when that zone exists, otherwise to the zone on screen.
    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQ_IN || resultCode != RESULT_OK || data == null || data.getData() == null) {
            return;
        }
        try (InputStream in = getContentResolver().openInputStream(data.getData())) {
            if (in == null) {
                return;
            }
            String body = read(in);
            String hint = Bat.zoneHint(body);
            String dest = hint.isEmpty() ? zoneId : hint;
            if (store.zone(dest) == null) {
                dest = zoneId;
            }
            store.replaceRepo(dest, Bat.parse(body));
            persist();
            Toast.makeText(this, R.string.imported, Toast.LENGTH_SHORT).show();
            show("list");
        } catch (Exception e) {
            Toast.makeText(this, e.getMessage(), Toast.LENGTH_SHORT).show();
        }
    }

    private static String read(InputStream in) throws Exception {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        byte[] b = new byte[4096];
        int n;
        while ((n = in.read(b)) > 0) {
            o.write(b, 0, n);
        }
        return o.toString(StandardCharsets.UTF_8.name());
    }

    private LinearLayout col() {
        return col(true);
    }

    private LinearLayout col(boolean pad) {
        LinearLayout l = new LinearLayout(this);
        l.setOrientation(LinearLayout.VERTICAL);
        if (pad) {
            int p = dp(16);
            l.setPadding(p, p / 2, p, p);
        }
        return l;
    }

    private LinearLayout row() {
        LinearLayout l = new LinearLayout(this);
        l.setOrientation(LinearLayout.HORIZONTAL);
        l.setGravity(Gravity.CENTER_VERTICAL);
        return l;
    }

    private ScrollView scroll(View v) {
        ScrollView s = new ScrollView(this);
        s.addView(v);
        return s;
    }

    private Button inflateBtn(int layout) {
        return (Button) getLayoutInflater().inflate(layout, findViewById(R.id.body), false);
    }

    private LinearLayout.LayoutParams stack(int top) {
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(top);
        return lp;
    }

    private Button chip(String t) {
        Button b = inflateBtn(R.layout.btn_chip);
        b.setText(t);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.rightMargin = dp(6);
        b.setLayoutParams(lp);
        return b;
    }

    private Button pill(String t) {
        Button b = chip(t);
        if (b instanceof MaterialButton) {
            ((MaterialButton) b).setCornerRadius(dp(18));
        }
        return b;
    }

    private Button go(String t, int top) {
        Button b = inflateBtn(R.layout.btn_go);
        b.setText(t);
        b.setLayoutParams(stack(top));
        return b;
    }

    private Button danger(String t, int top) {
        Button b = inflateBtn(R.layout.btn_danger);
        b.setText(t);
        b.setLayoutParams(stack(top));
        return b;
    }

    private Button ghostSm(String t, View.OnClickListener l) {
        Button b = inflateBtn(R.layout.btn_ghost_sm);
        b.setText(t);
        b.setOnClickListener(l);
        return b;
    }

    private Button mini(String t) {
        Button b = inflateBtn(R.layout.btn_mini);
        b.setText(t);
        return b;
    }

    private Button mini(String t, View.OnClickListener l) {
        Button b = mini(t);
        b.setOnClickListener(l);
        return b;
    }

    private EditText field(String v) {
        EditText e = new EditText(this);
        e.setText(v);
        e.setTextColor(getColor(R.color.snow));
        e.setHintTextColor(getColor(R.color.mute));
        e.setBackgroundResource(R.drawable.bg_field);
        e.setPadding(dp(12), dp(10), dp(12), dp(10));
        e.setTextSize(14);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(4);
        lp.bottomMargin = dp(12);
        e.setLayoutParams(lp);
        return e;
    }

    private EditText urlField(String v) {
        EditText e = field(v);
        e.setSingleLine(true);
        e.setEllipsize(TextUtils.TruncateAt.END);
        e.setTypeface(android.graphics.Typeface.MONOSPACE);
        e.setTextSize(12);
        e.setLayoutParams(new LinearLayout.LayoutParams(0, dp(40), 1));
        return e;
    }

    private LinearLayout urlRow(EditText url, View.OnClickListener lists) {
        LinearLayout row = row();
        LinearLayout.LayoutParams wrap = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        wrap.topMargin = dp(4);
        wrap.bottomMargin = dp(12);
        row.setLayoutParams(wrap);
        row.addView(url);
        Button pick = mini(getString(R.string.pick_list));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, dp(40));
        lp.leftMargin = dp(6);
        pick.setLayoutParams(lp);
        pick.setOnClickListener(lists);
        row.addView(pick);
        return row;
    }

    private LinearLayout autoRow(SplitStore.Zone z) {
        LinearLayout auto = row();
        auto.setPadding(0, dp(12), 0, dp(12));
        auto.setBackgroundResource(R.drawable.bg_setrow);
        LinearLayout text = col(false);
        TextView k = snow(getString(R.string.auto_update));
        k.setTextSize(14);
        TextView s = new TextView(this);
        s.setText(getString(R.string.auto_update_d));
        s.setTextColor(getColor(R.color.mute));
        s.setTextSize(11);
        s.setPadding(0, dp(2), 0, 0);
        text.addView(k);
        text.addView(s);
        auto.addView(text, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        ToggleView sw = new ToggleView(this);
        sw.setChecked(z.auto);
        sw.setOnCheckedChangeListener((v, on) -> {
            z.auto = on;
            persist();
        });
        LinearLayout.LayoutParams tlp = new LinearLayout.LayoutParams(dp(42), dp(26));
        tlp.leftMargin = dp(12);
        auto.addView(sw, tlp);
        return auto;
    }

    private TextView label(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        return v;
    }

    private TextView hint(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        v.setLineSpacing(0, 1.45f);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(10);
        v.setLayoutParams(lp);
        return v;
    }

    private TextView subline(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12.5f);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(12);
        v.setLayoutParams(lp);
        return v;
    }

    private TextView policyLine(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(10);
        v.setLayoutParams(lp);
        return v;
    }

    private TextView meta(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.stone));
        v.setTextSize(11);
        return v;
    }

    private TextView snow(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.snow));
        return v;
    }

    private LinearLayout.LayoutParams weight(int left) {
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                0, ViewGroup.LayoutParams.WRAP_CONTENT, 1);
        lp.leftMargin = dp(left);
        return lp;
    }

    private int dp(int v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }

    private static final class SimpleWatch implements android.text.TextWatcher {
        private final Runnable r;

        SimpleWatch(Runnable r) {
            this.r = r;
        }

        @Override
        public void beforeTextChanged(CharSequence s, int a, int b, int c) {
        }

        @Override
        public void onTextChanged(CharSequence s, int a, int b, int c) {
        }

        @Override
        public void afterTextChanged(android.text.Editable s) {
            r.run();
        }
    }
}
