/*
 * Android client, shared code: the in-app language switch. LoftApp applies
 * it at start, LoftActivity applies it to every screen, and the settings
 * screens (phone and TV) call apply() again after the user picks a language.
 */
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
 * XML layouts and getString() agree. Prefs is the source of truth: the
 * manifest does not enable AppCompat's "autoStoreLocales", so on Android 12
 * and older AppCompat does not remember the choice across restarts, and
 * LoftApp re-applies it from Prefs at every start.
 */
final class Lang {
    // Static helpers only; the class is never instantiated.
    /** Languages offered in the picker, in picker order. */
    static final String[] TAGS = {"en", "ru", "zh"};

    /**
     * Makes the stored choice the app-wide locale (AppCompat application
     * locales and the Java default locale). An empty choice hands the decision
     * back to the system. Call on the main thread: AppCompat may recreate
     * open activities when the locale actually changes, which is how the
     * settings screens switch language without a restart.
     */
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

    /**
     * Returns a context whose resources use the chosen language; the given
     * context unchanged when none is chosen. Called from
     * LoftActivity.attachBaseContext for every screen. Also sets the Java
     * default locale so number and date formatting match the UI language.
     */
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

    /**
     * The chosen language tag ("en", "ru", "zh"): Prefs first, then AppCompat's
     * application locale (which may have been set from the system's per-app
     * language settings), else empty meaning "follow the system".
     */
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

    /**
     * Position of the current language in {@link #TAGS} for the language
     * picker. With no choice stored it uses the device language; 0 (English)
     * when that is not one of the offered languages.
     */
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
