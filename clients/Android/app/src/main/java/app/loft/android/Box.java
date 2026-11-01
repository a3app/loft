/*
 * Android client, shared code: small encryption helper. Profiles uses it to
 * encrypt the saved connections file (which holds member keys and passwords)
 * with a key kept in the Android keystore, so the file is useless if copied
 * off the device.
 */
package app.loft.android;

import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;

import java.security.KeyStore;

import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

/**
 * AES-GCM in AndroidKeyStore. Falls back to plaintext if the store is missing.
 * AES-GCM both hides the text and detects tampering (the tag check fails).
 * Some devices ship a broken or missing keystore; the plaintext fallback
 * keeps the app working there instead of losing the user's connections.
 * The cipher itself runs in the keystore service (a system process), so each
 * call is IPC; the key handle is looked up once and cached. Profiles calls
 * seal() on its background writer thread, never on the main thread.
 */
final class Box {
    /** Keystore name of the app's AES key. */
    private static final String ALIAS = "app.loft.profiles";
    /** Name of Android's system keystore provider. */
    private static final String KS = "AndroidKeyStore";
    /** Cipher transformation: AES in GCM mode (GCM needs no padding). */
    private static final String TF = "AES/GCM/NoPadding";
    /** GCM nonce length in bytes. */
    private static final int IV = 12;
    /** GCM authentication tag length in bits. */
    private static final int TAG = 128;
    /**
     * The keystore key handle, looked up once. Loading the keystore and
     * finding the alias are calls into a system process; doing them for
     * every seal/open was pure waste. Reset when the key stops working.
     */
    private static volatile SecretKey cached;

    /**
     * Encrypts text. Output is "v1:" + base64(IV || ciphertext+tag), or
     * "p:" + plain text when the keystore is not usable on this device.
     */
    static String seal(String plain) {
        if (plain == null) {
            plain = "";
        }
        // Two tries: the first may fail on a stale cached key handle or a
        // keystore that is still starting (seen on Xiaomi right after boot).
        for (int attempt = 0; attempt < 2; attempt++) {
            try {
                SecretKey key = key();
                Cipher c = Cipher.getInstance(TF);
                // The keystore picks a fresh random IV itself (callers may not
                // supply one), so it is read back and stored with the ciphertext.
                c.init(Cipher.ENCRYPT_MODE, key);
                byte[] iv = c.getIV();
                byte[] ct = c.doFinal(plain.getBytes("UTF-8"));
                byte[] out = new byte[iv.length + ct.length];
                System.arraycopy(iv, 0, out, 0, iv.length);
                System.arraycopy(ct, 0, out, iv.length, ct.length);
                return "v1:" + Base64.encodeToString(out, Base64.NO_WRAP);
            } catch (Exception e) {
                cached = null;
                pause();
            }
        }
        return "p:" + plain;
    }

    /**
     * Reverses seal(). Text without a known prefix is returned as is (files
     * written before encryption was added); a blob that fails to decrypt gives "".
     */
    static String open(String blob) {
        String s = openOrNull(blob);
        return s == null ? "" : s;
    }

    /**
     * Like open(), but returns null when a "v1:" blob cannot be decrypted, so
     * the caller can tell "nothing saved" from "saved but unreadable right
     * now" (keystore busy or reset). Profiles relies on this to never
     * overwrite a file it could not read.
     */
    static String openOrNull(String blob) {
        if (blob == null || blob.isEmpty()) {
            return "";
        }
        if (blob.startsWith("p:")) {
            return blob.substring(2);
        }
        if (!blob.startsWith("v1:")) {
            return blob;
        }
        byte[] raw;
        try {
            raw = Base64.decode(blob.substring(3), Base64.NO_WRAP);
        } catch (Exception e) {
            return null;
        }
        if (raw.length <= IV) {
            return null;
        }
        for (int attempt = 0; attempt < 2; attempt++) {
            try {
                SecretKey key = key();
                Cipher c = Cipher.getInstance(TF);
                GCMParameterSpec spec = new GCMParameterSpec(TAG, raw, 0, IV);
                c.init(Cipher.DECRYPT_MODE, key, spec);
                byte[] pt = c.doFinal(raw, IV, raw.length - IV);
                return new String(pt, "UTF-8");
            } catch (javax.crypto.AEADBadTagException e) {
                // Wrong key or damaged data: retrying cannot help.
                return null;
            } catch (Exception e) {
                cached = null;
                pause();
            }
        }
        return null;
    }

    /** Short wait between the two tries above. */
    private static void pause() {
        try {
            Thread.sleep(150);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    // The AES-256 key lives in the Android keystore (hardware-backed where the
    // device supports it) and never leaves it; it is created on first use.
    // If the key is ever lost (app data cleared, keystore reset), old "v1:"
    // blobs can no longer be opened and open() returns "".
    private static SecretKey key() throws Exception {
        SecretKey k = cached;
        if (k != null) {
            return k;
        }
        k = lookup();
        cached = k;
        return k;
    }

    /** Finds the key in the keystore, creating it on first use. */
    private static SecretKey lookup() throws Exception {
        KeyStore ks = KeyStore.getInstance(KS);
        ks.load(null);
        if (ks.containsAlias(ALIAS)) {
            KeyStore.SecretKeyEntry e = (KeyStore.SecretKeyEntry) ks.getEntry(ALIAS, null);
            if (e != null) {
                return e.getSecretKey();
            }
        }
        KeyGenerator g = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KS);
        g.init(new KeyGenParameterSpec.Builder(ALIAS,
                KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build());
        return g.generateKey();
    }
}
