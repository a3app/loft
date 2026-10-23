package app.loft.android;

import android.content.Context;
import android.content.res.Configuration;
import android.os.LocaleList;

import androidx.appcompat.app.AppCompatDelegate;
import androidx.core.os.LocaleListCompat;

import java.util.Locale;

final class Lang {
    static final String[] TAGS = {"en", "ru", "zh"};

    static void apply(Context c) {
        String tag = tag(c);
        LocaleListCompat list = tag.isEmpty()
                ? LocaleListCompat.getEmptyLocaleList()
                : LocaleListCompat.forLanguageTags(tag);
        AppCompatDelegate.setApplicationLocales(list);
        if (!tag.isEmpty()) {
            Locale.setDefault(Locale.forLanguageTag(tag));
        }
    }

    static Context wrap(Context c) {
        String tag = tag(c);
        if (tag.isEmpty()) {
            return c;
        }
        Locale loc = Locale.forLanguageTag(tag);
        Locale.setDefault(loc);
        Configuration conf = new Configuration(c.getResources().getConfiguration());
        conf.setLocales(new LocaleList(loc));
        return c.createConfigurationContext(conf);
    }

    static String tag(Context c) {
        String tag = Prefs.lang(c);
        if (tag != null && !tag.isEmpty()) {
            return tag;
        }
        LocaleListCompat app = AppCompatDelegate.getApplicationLocales();
        if (app != null && !app.isEmpty()) {
            Locale loc = app.get(0);
            if (loc != null && loc.getLanguage() != null) {
                return loc.getLanguage();
            }
        }
        return "";
    }

    static int index(Context c) {
        String tag = tag(c);
        if (tag.isEmpty()) {
            tag = c.getResources().getConfiguration().getLocales().get(0).getLanguage();
        }
        for (int i = 0; i < TAGS.length; i++) {
            if (TAGS[i].equalsIgnoreCase(tag)) {
                return i;
            }
        }
        return 0;
    }
}
