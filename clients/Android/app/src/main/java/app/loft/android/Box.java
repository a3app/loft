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

/** AES-GCM in AndroidKeyStore. Falls back to plaintext if the store is missing. */
final class Box {
    /** Keystore name of the app's AES key. */
    private static final String ALIAS = "app.loft.profiles";
    private static final String KS = "AndroidKeyStore";
    private static final String TF = "AES/GCM/NoPadding";
    /** GCM nonce length in bytes. */
    private static final int IV = 12;
    /** GCM authentication tag length in bits. */
    private static final int TAG = 128;

    /**
     * Encrypts text. Output is "v1:" + base64(IV || ciphertext+tag), or
     * "p:" + plain text when the keystore is not usable on this device.
     */
    static String seal(String plain) {
        if (plain == null) {
            plain = "";
        }
        try {
            SecretKey key = key();
            Cipher c = Cipher.getInstance(TF);
            c.init(Cipher.ENCRYPT_MODE, key);
            byte[] iv = c.getIV();
            byte[] ct = c.doFinal(plain.getBytes("UTF-8"));
            byte[] out = new byte[iv.length + ct.length];
            System.arraycopy(iv, 0, out, 0, iv.length);
            System.arraycopy(ct, 0, out, iv.length, ct.length);
            return "v1:" + Base64.encodeToString(out, Base64.NO_WRAP);
        } catch (Exception e) {
            return "p:" + plain;
        }
    }

    /**
     * Reverses seal(). Text without a known prefix is returned as is (files
     * written before encryption was added); a blob that fails to decrypt gives "".
     */
    static String open(String blob) {
        if (blob == null || blob.isEmpty()) {
            return "";
        }
        if (blob.startsWith("p:")) {
            return blob.substring(2);
        }
        if (!blob.startsWith("v1:")) {
            return blob;
        }
        try {
            byte[] raw = Base64.decode(blob.substring(3), Base64.NO_WRAP);
            if (raw.length <= IV) {
                return "";
            }
            SecretKey key = key();
            Cipher c = Cipher.getInstance(TF);
            GCMParameterSpec spec = new GCMParameterSpec(TAG, raw, 0, IV);
            c.init(Cipher.DECRYPT_MODE, key, spec);
            byte[] pt = c.doFinal(raw, IV, raw.length - IV);
            return new String(pt, "UTF-8");
        } catch (Exception e) {
            return "";
        }
    }

    // The AES-256 key lives in the hardware-backed keystore and never leaves it;
    // it is created on first use.
    private static SecretKey key() throws Exception {
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
