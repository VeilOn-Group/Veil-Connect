plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

// Values baked into the build (public data only: root PUBLIC keys and API pins).
val rootKeys: String = providers.gradleProperty("veilon.rootKeys").getOrElse("")
val apiBaseUrls: String = providers.gradleProperty("veilon.apiBaseUrls").getOrElse("https://api.veilon.example")
val apiPins: String = providers.gradleProperty("veilon.apiPins").getOrElse("")

// Versions shared with the native core build (toolchain.lock, ADR-0061).
val toolchain: Map<String, String> = rootProject.file("toolchain.lock").readLines()
    .map { it.trim() }
    .filter { it.isNotEmpty() && !it.startsWith("#") && it.contains('=') }
    .associate { it.substringBefore('=') to it.substringAfter('=') }

android {
    namespace = "net.veilon.app"
    compileSdk = toolchain.getValue("ANDROID_PLATFORM").removePrefix("android-").toInt()
    buildToolsVersion = toolchain.getValue("ANDROID_BUILD_TOOLS")
    ndkVersion = toolchain.getValue("ANDROID_NDK_VERSION")

    defaultConfig {
        applicationId = "net.veilon.app"
        minSdk = toolchain.getValue("ANDROID_MIN_API").toInt()
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
        buildConfigField("String", "ROOT_KEYS", "\"$rootKeys\"")
        buildConfigField("String", "API_BASE_URLS", "\"$apiBaseUrls\"")
        buildConfigField("String", "API_PINS", "\"$apiPins\"")
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    // libxray.so (scripts/fetch-xray.sh) is executed as a child process, so it
    // must be extracted to nativeLibraryDir (ADR-0066).
    packaging { jniLibs { useLegacyPackaging = true } }
}

dependencies {
    // Built by scripts/build-aar.sh (gomobile, toolchain.lock): sing-box
    // libbox (GPL-3.0) and the VeilOn bridge veil-shared/mobile in ONE AAR
    // (net.veilon.core.libbox.*, net.veilon.core.mobile.*; ADR-0061).
    implementation(files("libs/veilon-core.aar"))

    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.compose.material3)
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.icons)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.service)
    implementation(libs.androidx.security.crypto)
    implementation(libs.okhttp)
    implementation(libs.kotlinx.coroutines.android)
    testImplementation(libs.junit)
}
