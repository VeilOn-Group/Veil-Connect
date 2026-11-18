package net.veilon.app.data

import okhttp3.CertificatePinner
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.io.IOException
import java.net.URI
import java.util.concurrent.TimeUnit

class ApiException(val status: Int, val code: String, message: String) : IOException(message)

/**
 * VeilOn API client: TLS with SPKI pinning (current + backup pin), several
 * base URLs tried in order. No identifiers beyond the bearer token
 * are sent; OkHttp has no cache and no cookie jar.
 */
class ApiClient(private val baseUrls: List<String>, pins: List<String>) {
    private val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .certificatePinner(
            CertificatePinner.Builder().apply {
                if (pins.isNotEmpty()) baseUrls.forEach { u -> pins.forEach { p -> add(URI(u).host, "sha256/$p") } }
            }.build()
        )
        .build()
    private var preferred = 0
    private val json = "application/json".toMediaType()

    @Throws(IOException::class)
    fun call(method: String, path: String, body: String? = null, bearer: String? = null): String {
        var last: IOException? = null
        for (i in baseUrls.indices) {
            val idx = (preferred + i) % baseUrls.size
            val req = Request.Builder()
                .url(baseUrls[idx].trimEnd('/') + path)
                .method(method, body?.toRequestBody(json))
                .header("Accept", "application/json")
                .apply { if (bearer != null) header("Authorization", "Bearer $bearer") }
                .build()
            try {
                http.newCall(req).execute().use { resp ->
                    val text = resp.body?.string().orEmpty()
                    if (!resp.isSuccessful) {
                        val o = runCatching { JSONObject(text) }.getOrNull()
                        throw ApiException(resp.code, o?.optString("code") ?: "error", o?.optString("message") ?: resp.message)
                    }
                    preferred = idx
                    return text
                }
            } catch (e: ApiException) {
                throw e // our API answered: do not rotate
            } catch (e: IOException) {
                last = e
            }
        }
        throw last ?: IOException("no API endpoints configured")
    }
}
