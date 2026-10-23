package app.loft.android;

import android.app.AlertDialog;
import android.content.Intent;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Switch;
import android.widget.TextView;
import android.widget.Toast;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.UUID;

public final class ListsActivity extends LoftActivity {
    private static final int REQ_IN = 8;
    private SplitStore store;
    private String zoneId = SplitStore.GLOBAL;
    private String page = "list";
    private String editing;
    private final Handler main = new Handler(Looper.getMainLooper());

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_lists);
        store = SplitStore.load(this);
        Switch split = findViewById(R.id.sw_split);
        split.setChecked(Prefs.split(this));
        split.setOnCheckedChangeListener((v, on) -> Prefs.p(this).edit().putBoolean("split", on).apply());
        findViewById(R.id.btn_back).setOnClickListener(v -> onBackPressed());
        show("list");
    }

    @Override
    public void onBackPressed() {
        if (!"list".equals(page)) {
            show("list");
            return;
        }
        super.onBackPressed();
    }

    private void persist() {
        store.save(this);
    }

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

    private View listPage() {
        LinearLayout root = col();
        LinearLayout chips = new LinearLayout(this);
        chips.setOrientation(LinearLayout.HORIZONTAL);
        for (SplitStore.Zone z : store.zones) {
            Button b = chip(z.name + " " + store.count(z.id));
            b.setSelected(z.id.equals(zoneId));
            b.setOnClickListener(v -> {
                zoneId = z.id;
                show("list");
            });
            chips.addView(b);
        }
        Button zones = chip(getString(R.string.zones));
        zones.setOnClickListener(v -> show("zones"));
        chips.addView(zones);
        HorizontalScrollView hs = new HorizontalScrollView(this);
        hs.setHorizontalScrollBarEnabled(false);
        hs.addView(chips);
        root.addView(hs);
        SplitStore.Zone z = store.zone(zoneId);
        TextView policy = mute(z != null && z.through
                ? getString(R.string.policy_through, z.name)
                : getString(R.string.policy_around, z == null ? zoneId : z.name));
        root.addView(policy);
        EditText search = field("");
        search.setHint(getString(R.string.search));
        search.setInputType(android.text.InputType.TYPE_CLASS_TEXT);
        root.addView(search);
        LinearLayout list = col();
        Runnable paint = () -> {
            list.removeAllViews();
            List<SplitStore.Svc> rows = store.in(zoneId, search.getText().toString());
            if (rows.isEmpty()) {
                list.addView(mute(getString(R.string.empty_zone)));
                return;
            }
            for (SplitStore.Svc s : rows) {
                list.addView(svcRow(s));
            }
        };
        search.addTextChangedListener(new SimpleWatch(paint));
        paint.run();
        ScrollView sc = new ScrollView(this);
        sc.addView(list);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1);
        root.addView(sc, lp);
        Button add = go(getString(R.string.add));
        LinearLayout.LayoutParams addLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        addLp.topMargin = dp(8);
        add.setLayoutParams(addLp);
        add.setOnClickListener(v -> new AlertDialog.Builder(this)
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
        root.addView(add);
        LinearLayout foot = row();
        foot.addView(ghost(getString(R.string.import_routes), v -> {
            Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
            i.addCategory(Intent.CATEGORY_OPENABLE);
            i.setType("*/*");
            startActivityForResult(i, REQ_IN);
        }), weight());
        foot.addView(ghost(getString(R.string.export_routes), v -> {
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, Bat.emit(store, zoneId));
            i.putExtra(Intent.EXTRA_SUBJECT, zoneId + ".bat");
            startActivity(Intent.createChooser(i, getString(R.string.export_routes)));
        }), weight());
        foot.addView(ghost(getString(R.string.repository), v -> show("repo")), weight());
        root.addView(foot);
        return root;
    }

    private View svcRow(SplitStore.Svc s) {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setBackgroundResource(R.drawable.bg_card);
        row.setPadding(dp(12), dp(10), dp(12), dp(10));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(6);
        row.setLayoutParams(lp);
        LinearLayout grow = col();
        TextView n = new TextView(this);
        n.setText(s.name);
        n.setTextColor(getColor(R.color.snow));
        n.setTextSize(13.5f);
        TextView m = new TextView(this);
        m.setText(SplitStore.join(s.cidrs));
        m.setTextColor(getColor(R.color.mute));
        m.setTextSize(10.5f);
        m.setTypeface(android.graphics.Typeface.MONOSPACE);
        grow.addView(n);
        grow.addView(m);
        row.addView(grow, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        TextView src = mute(s.ignore ? getString(R.string.ignore)
                : "repo".equals(s.src) ? getString(R.string.from_repo) : getString(R.string.mine));
        row.addView(src);
        row.setOnClickListener(v -> {
            editing = s.id;
            show("svc");
        });
        return row;
    }

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
        root.addView(kind);
        Button save = go(getString(R.string.save));
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
            Button del = danger(getString(R.string.delete));
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
            row.addView(mute((z.through ? getString(R.string.through_loft) : getString(R.string.around_loft))
                    + " · " + store.count(z.id)));
            row.setOnClickListener(v -> {
                editing = z.id;
                show("zone");
            });
            root.addView(row);
        }
        Button add = go(getString(R.string.new_zone));
        add.setOnClickListener(v -> {
            SplitStore.Zone z = new SplitStore.Zone();
            z.id = UUID.randomUUID().toString();
            z.name = getString(R.string.new_zone);
            z.through = true;
            store.zones.add(z);
            persist();
            editing = z.id;
            show("zone");
        });
        root.addView(add);
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
        root.addView(dir);
        root.addView(label(getString(R.string.list_url)));
        LinearLayout urlRow = row();
        EditText url = field(z.repo);
        urlRow.addView(url, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        Button lists = chip(getString(R.string.pick_list));
        lists.setOnClickListener(v -> show("catalog"));
        urlRow.addView(lists);
        root.addView(urlRow);
        LinearLayout auto = row();
        TextView al = snow(getString(R.string.auto_update));
        auto.addView(al, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        Switch sw = new Switch(this);
        sw.setChecked(z.auto);
        auto.addView(sw);
        root.addView(auto);
        Button save = go(getString(R.string.save));
        save.setOnClickListener(v -> {
            if (!z.builtin) {
                z.name = name.getText().toString().trim();
            }
            z.repo = url.getText().toString().trim();
            z.auto = sw.isChecked();
            persist();
            show("zones");
        });
        root.addView(save);
        if (!z.builtin) {
            Button del = danger(getString(R.string.delete_zone));
            del.setOnClickListener(v -> {
                store.deleteZone(z.id);
                persist();
                show("zones");
            });
            root.addView(del);
        }
        return scroll(root);
    }

    private View repoPage() {
        SplitStore.Zone z = store.zone(zoneId);
        LinearLayout root = col();
        if (z == null) {
            return root;
        }
        root.addView(label(getString(R.string.list_url)));
        LinearLayout urlRow = row();
        EditText url = field(z.repo);
        urlRow.addView(url, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        Button lists = chip(getString(R.string.pick_list));
        lists.setOnClickListener(v -> {
            z.repo = url.getText().toString().trim();
            persist();
            show("catalog");
        });
        urlRow.addView(lists);
        root.addView(urlRow);
        LinearLayout auto = row();
        auto.addView(snow(getString(R.string.auto_update)),
                new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        Switch sw = new Switch(this);
        sw.setChecked(z.auto);
        auto.addView(sw);
        root.addView(auto);
        Button pull = go(getString(R.string.pull));
        pull.setOnClickListener(v -> {
            z.repo = url.getText().toString().trim();
            z.auto = sw.isChecked();
            persist();
            pull(z);
        });
        root.addView(pull);
        Button save = ghost(getString(R.string.save), v -> {
            z.repo = url.getText().toString().trim();
            z.auto = sw.isChecked();
            persist();
            show("list");
        });
        root.addView(save);
        return scroll(root);
    }

    private View catalogPage() {
        LinearLayout root = col();
        String lang = Prefs.applyLang(this);
        for (String[] row : SplitStore.CATALOG) {
            Button b = go(SplitStore.catalogLabel(row[0], lang));
            b.setOnClickListener(v -> {
                SplitStore.Zone z = store.zone(zoneId);
                if (z != null) {
                    z.repo = SplitStore.catalogUrl(row[0]);
                    persist();
                }
                show("repo");
            });
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            lp.bottomMargin = dp(6);
            b.setLayoutParams(lp);
            root.addView(b);
        }
        return scroll(root);
    }

    private void pull(SplitStore.Zone z) {
        if (z.repo == null || z.repo.isEmpty()) {
            Toast.makeText(this, R.string.no_url, Toast.LENGTH_SHORT).show();
            return;
        }
        new Thread(() -> {
            try {
                HttpURLConnection c = (HttpURLConnection) new URL(z.repo).openConnection();
                c.setConnectTimeout(8000);
                c.setReadTimeout(8000);
                try (InputStream in = c.getInputStream()) {
                    String body = read(in);
                    List<Bat.Row> rows = Bat.parse(body);
                    main.post(() -> {
                        store.replaceRepo(z.id, rows);
                        persist();
                        Toast.makeText(this, R.string.pulled, Toast.LENGTH_SHORT).show();
                        show("list");
                    });
                }
            } catch (Exception e) {
                main.post(() -> Toast.makeText(this, R.string.pull_fail, Toast.LENGTH_SHORT).show());
            }
        }).start();
    }

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
        LinearLayout l = new LinearLayout(this);
        l.setOrientation(LinearLayout.VERTICAL);
        int p = dp(16);
        l.setPadding(p, p / 2, p, p);
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

    private Button chip(String t) {
        Button b = new Button(this, null, 0, R.style.Widget_Loft_Chip);
        b.setText(t);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.rightMargin = dp(6);
        b.setLayoutParams(lp);
        return b;
    }

    private Button go(String t) {
        Button b = new Button(this, null, 0, R.style.Widget_Loft_Go);
        b.setText(t);
        return b;
    }

    private Button danger(String t) {
        Button b = new Button(this, null, 0, R.style.Widget_Loft_Danger);
        b.setText(t);
        return b;
    }

    private Button ghost(String t, View.OnClickListener l) {
        Button b = new Button(this, null, 0, R.style.Widget_Loft_Ghost);
        b.setText(t);
        b.setTextSize(12);
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
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(10);
        e.setLayoutParams(lp);
        return e;
    }

    private TextView label(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        return v;
    }

    private TextView mute(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        v.setPadding(0, dp(6), 0, dp(8));
        return v;
    }

    private TextView snow(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.snow));
        return v;
    }

    private LinearLayout.LayoutParams weight() {
        return new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1);
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
