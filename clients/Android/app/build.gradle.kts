plugins {
    id("com.android.application")
}

val androidDist = rootProject.projectDir.resolve("../../dist/clients/Android").canonicalFile
val loftVersion = rootProject.projectDir.resolve("../../VERSION").readText().trim()

fun loftVersionCode(v: String): Int {
    val core = v.substringBefore('-')
    val p = core.split('.')
    fun n(i: Int) = p.getOrNull(i)?.toIntOrNull() ?: 0
    return n(0) * 1_000_000 + n(1) * 1_000 + n(2)
}

/** Java string literal for a BuildConfig field; safe against Gradle `$` interpolation. */
fun javaStringLiteral(s: String): String {
    val b = StringBuilder("\"")
    for (ch in s) {
        when (ch) {
            '\\' -> b.append("\\\\")
            '"' -> b.append("\\\"")
            '\n' -> b.append("\\n")
            '\r' -> b.append("\\r")
            '\t' -> b.append("\\t")
            '$' -> b.append("\\u0024")
            else -> if (ch.code < 0x20) b.append("\\u%04x".format(ch.code)) else b.append(ch)
        }
    }
    b.append('"')
    return b.toString()
}

layout.buildDirectory.set(androidDist.resolve("gradle/app"))

android {
    namespace = "app.loft.android"
    compileSdk = 36
    defaultConfig {
        minSdk = 26
        targetSdk = 36
        versionCode = loftVersionCode(loftVersion)
        versionName = loftVersion
    }
    buildFeatures {
        buildConfig = true
    }
    flavorDimensions += listOf("device", "channel")
    productFlavors {
        create("phone") {
            dimension = "device"
            isDefault = true
            applicationId = "app.loft.android"
        }
        create("tv") {
            dimension = "device"
            applicationId = "app.loft.android.tv"
        }
        create("github") {
            dimension = "channel"
            isDefault = true
            buildConfigField("boolean", "GITHUB", "true")
            // issues:write only — this string is in the APK. Empty: reports stay queued.
            buildConfigField(
                "String",
                "CRASH_TOKEN",
                javaStringLiteral(System.getenv("LOFT_ANDROID_CRASH_TOKEN") ?: ""),
            )
        }
        create("store") {
            dimension = "channel"
            buildConfigField("boolean", "GITHUB", "false")
        }
    }
    signingConfigs {
        create("release") {
            val store = System.getenv("LOFT_STORE_FILE")
            if (!store.isNullOrBlank() && file(store).isFile) {
                storeFile = file(store)
                storePassword = System.getenv("LOFT_STORE_PASSWORD") ?: ""
                keyAlias = System.getenv("LOFT_KEY_ALIAS") ?: "loft"
                keyPassword = System.getenv("LOFT_KEY_PASSWORD") ?: storePassword
            }
        }
    }
    buildTypes {
        release {
            isMinifyEnabled = false
            val rel = signingConfigs.getByName("release")
            if (rel.storeFile != null) {
                signingConfig = rel
            }
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }
}

dependencies {
    implementation(files("../../mobile/build/engine.aar"))
    implementation("androidx.appcompat:appcompat:1.8.0")
    implementation("androidx.recyclerview:recyclerview:1.4.0")
    implementation("com.google.android.material:material:1.14.0")
    implementation("com.google.zxing:core:3.5.4")
    add("phoneImplementation", "androidx.camera:camera-core:1.4.1")
    add("phoneImplementation", "androidx.camera:camera-camera2:1.4.1")
    add("phoneImplementation", "androidx.camera:camera-lifecycle:1.4.1")
    add("phoneImplementation", "androidx.camera:camera-view:1.4.1")
    add("tvImplementation", "androidx.leanback:leanback:1.2.0")
    testImplementation("junit:junit:4.13.2")
}

class EmuSpec(
    val dirName: String,
    val bundleId: String,
    val name: String,
    val displayName: String,
    val variant: String,
)

val emuSpecs = listOf(
    EmuSpec("loft.app", "app.loft.android.emulator", "loft", "loft", "phone"),
    EmuSpec("loft-tv.app", "app.loft.android.emulator.tv", "loft tv", "loft tv", "tv"),
    EmuSpec("loft-gtv.app", "app.loft.android.emulator.gtv", "loft gtv", "loft gtv", "gtv"),
)

fun packEmulatorApp(apkDir: File, versionName: String, versionCode: Int, spec: EmuSpec) {
    val src = rootProject.projectDir.resolve("emulator")
    val script = src.resolve("loft")
    val plistIn = src.resolve("Info.plist")
    require(script.isFile) { "missing ${script}" }
    require(plistIn.isFile) { "missing ${plistIn}" }
    val bundle = apkDir.resolve(spec.dirName)
    if (bundle.exists()) bundle.deleteRecursively()
    val contents = bundle.resolve("Contents")
    val macos = contents.resolve("MacOS")
    val resources = contents.resolve("Resources")
    macos.mkdirs()
    resources.mkdirs()
    script.copyTo(macos.resolve("loft"))
    macos.resolve("loft").setExecutable(true, false)
    resources.resolve("variant").writeText(spec.variant)
    val plist = plistIn.readText()
        .replace("__LOFT_BUNDLE_ID__", spec.bundleId)
        .replace("__LOFT_NAME__", spec.name)
        .replace("__LOFT_DISPLAY_NAME__", spec.displayName)
        .replace("__LOFT_VERSION__", versionName)
        .replace("__LOFT_CODE__", versionCode.toString())
    contents.resolve("Info.plist").writeText(plist)
    contents.resolve("PkgInfo").writeText("APPL????")
    val icns = rootProject.projectDir.resolve("../macOS/Resources/loft.icns")
    if (icns.isFile) icns.copyTo(resources.resolve("loft.icns"))
    try {
        ProcessBuilder("xattr", "-cr", bundle.absolutePath).start().waitFor()
    } catch (_: Exception) {
    }
}

fun newestGo(root: File): Long {
    var t = 0L
    if (!root.isDirectory) {
        return t
    }
    root.walkTopDown()
        .onEnter { it.name != "build" && it.name != "vendor" }
        .forEach { f ->
            if (f.isFile && f.extension == "go" && f.lastModified() > t) {
                t = f.lastModified()
            }
        }
    return t
}

tasks.register("checkEngineAar") {
    doLast {
        val aar = project.projectDir.resolve("../../mobile/build/engine.aar")
        val msg = "engine.aar is missing or older than Go sources; run: make -C clients/mobile android"
        if (!aar.isFile) {
            throw GradleException(msg)
        }
        val roots = listOf(
            project.projectDir.resolve("../../mobile"),
            project.projectDir.resolve("../../../internal/vless"),
            project.projectDir.resolve("../../../internal/xtls"),
            project.projectDir.resolve("../../../internal/protect"),
            project.projectDir.resolve("../../go/inbound"),
        )
        var newest = 0L
        roots.forEach { n ->
            val t = newestGo(n)
            if (t > newest) {
                newest = t
            }
        }
        if (aar.lastModified() < newest) {
            throw GradleException(msg)
        }
    }
}

afterEvaluate {
    tasks.matching {
        it.name.startsWith("assemble") || it.name.startsWith("install")
    }.configureEach {
        dependsOn("checkEngineAar")
    }
    val phoneApk = layout.buildDirectory.file(
        "outputs/apk/phoneGithub/debug/app-phone-github-debug.apk"
    )
    val tvApk = layout.buildDirectory.file(
        "outputs/apk/tvGithub/debug/app-tv-github-debug.apk"
    )
    tasks.register("distDebug") {
        dependsOn("packagePhoneGithubDebug", "packageTvGithubDebug")
        inputs.file(phoneApk)
        inputs.file(tvApk)
        inputs.dir(rootProject.projectDir.resolve("emulator"))
        outputs.file(androidDist.resolve("app-debug.apk"))
        outputs.file(androidDist.resolve("app-tv-debug.apk"))
        emuSpecs.forEach { outputs.dir(androidDist.resolve(it.dirName)) }
        doLast {
            androidDist.mkdirs()
            phoneApk.get().asFile.copyTo(androidDist.resolve("app-debug.apk"), overwrite = true)
            tvApk.get().asFile.copyTo(androidDist.resolve("app-tv-debug.apk"), overwrite = true)
            val ver = android.defaultConfig.versionName!!
            val code = android.defaultConfig.versionCode!!
            emuSpecs.forEach { packEmulatorApp(androidDist, ver, code, it) }
        }
    }
    tasks.register("packageEmulatorApp") {
        dependsOn("distDebug")
    }
    tasks.named("assemblePhoneGithubDebug") {
        finalizedBy("distDebug")
    }
    tasks.named("assembleTvGithubDebug") {
        finalizedBy("distDebug")
    }
}
