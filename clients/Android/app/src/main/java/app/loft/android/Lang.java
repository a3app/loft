package app.loft.android;

import android.content.Context;
import android.content.res.Configuration;
import android.os.LocaleList;

import androidx.appcompat.app.AppCompatDelegate;
import androidx.core.os.LocaleListCompat;

import java.util.Locale;

/**
 * In-app language choice (English, Russian, Chinese). An empty choice means
 * "follow the system". The choice is stored in Prefs and applied both to
 * AppCompat (application locales) and to each activity's base context, so
 * XML layouts and getString() agree.
 */
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

    /** Returns a context whose resources use the chosen language; the system context when none is chosen. */
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

    /** The chosen language tag: Prefs first, then AppCompat's application locale, else empty. */
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

    /** Position of the current language in {@link #TAGS} for the language picker; 0 (English) when unknown. */
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
