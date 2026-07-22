package uk.co.dvhub.android

import com.google.gson.Gson
import com.google.gson.JsonObject
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import okio.ByteString
import java.util.concurrent.TimeUnit

class GatewayClient(private val listener: Listener) {
    interface Listener {
        fun onConnection(connected: Boolean, message: String)
        fun onJson(json: JsonObject)
        fun onAudio(bytes: ByteArray)
    }

    private val gson = Gson()
    private var settings = GatewaySettings()
    private var socket: WebSocket? = null
    private var client = makeClient()

    private fun makeClient() = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS).readTimeout(15, TimeUnit.SECONDS)
        .pingInterval(20, TimeUnit.SECONDS).build()

    fun configure(value: GatewaySettings) {
        settings = value.copy(serverUrl = value.serverUrl.trimEnd('/'))
        socket?.close(1000, "Reconfigure")
        client = makeClient()
    }

    private fun request(path: String, body: RequestBody? = null, control: Boolean = false): Request {
        val credentials = Credentials.basic(settings.username, settings.password)
        return Request.Builder().url(settings.serverUrl + path).header("Authorization", credentials).apply {
            if (control) header("X-DVHub-Control", "1")
            if (body == null) get() else post(body)
        }.build()
    }

    fun connect() {
        if (settings.password.isBlank()) {
            listener.onConnection(false, "Enter the gateway password in Settings")
            return
        }
        val wsUrl = settings.serverUrl.replaceFirst("https://", "wss://").replaceFirst("http://", "ws://") + "/ws"
        val req = Request.Builder().url(wsUrl).header("Authorization", Credentials.basic(settings.username, settings.password)).build()
        socket = client.newWebSocket(req, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) = listener.onConnection(true, "Connected")
            override fun onMessage(webSocket: WebSocket, text: String) {
                runCatching { gson.fromJson(text, JsonObject::class.java) }.onSuccess(listener::onJson)
            }
            override fun onMessage(webSocket: WebSocket, bytes: ByteString) = listener.onAudio(bytes.toByteArray())
            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = listener.onConnection(false, "Disconnected")
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) =
                listener.onConnection(false, response?.let { "Connection failed (${it.code})" } ?: (t.message ?: "Connection failed"))
        })
    }

    fun close() { socket?.close(1000, "App stopped"); socket = null }
    fun send(json: JsonObject): Boolean = socket?.send(gson.toJson(json)) == true
    fun sendAudio(bytes: ByteArray): Boolean = socket?.send(ByteString.of(*bytes)) == true

    fun get(path: String, done: (Result<JsonObject>) -> Unit) {
        client.newCall(request(path)).enqueue(jsonCallback(done))
    }

    fun post(path: String, json: JsonObject, done: (Result<JsonObject>) -> Unit) {
        val body = gson.toJson(json).toRequestBody("application/json".toMediaType())
        client.newCall(request(path, body, true)).enqueue(jsonCallback(done))
    }

    private fun jsonCallback(done: (Result<JsonObject>) -> Unit) = object : Callback {
        override fun onFailure(call: Call, e: java.io.IOException) = done(Result.failure(e))
        override fun onResponse(call: Call, response: Response) {
            response.use {
                val text = it.body.string()
                if (!it.isSuccessful) done(Result.failure(IllegalStateException("HTTP ${it.code}: $text")))
                else done(runCatching { gson.fromJson(text, JsonObject::class.java) })
            }
        }
    }

    fun nodeState(network: Network, active: Boolean, tg: Int, password: String, options: String = "") = send(JsonObject().apply {
		val effectiveOptions = if (network.nodeId == 7 && network.target == "FreeSTAR-SystemX-UK") {
			if (tg == 4000) "TS2_1=0;" else "TS2_1=$tg;"
		} else options
		val effectiveDMRId = if (network.nodeId == 7 && network.target == "FreeSTAR-SystemX-UK") 2351633L else (settings.dmrId.toLongOrNull() ?: 0)
		val effectiveRepeaterId = if (network.nodeId == 7 && network.target == "FreeSTAR-SystemX-UK") {
			effectiveDMRId * 100 + 2
		} else repeaterId()
        addProperty("cmd", "node_state"); addProperty("node_id", network.nodeId); addProperty("active", active)
        addProperty("mode", if (network.apiName == "ysf") "YSF" else "DMR"); addProperty("target", network.target)
        addProperty("tg", tg); addProperty("password", password); addProperty("callsign", settings.callsign)
        addProperty("dmr_id", effectiveDMRId); addProperty("repeater_id", effectiveRepeaterId)
        addProperty("options", effectiveOptions)
    })

    fun txStart(nodeId: Int) = send(JsonObject().apply { addProperty("cmd", "tx_start"); addProperty("node_id", nodeId) })
    fun txStop() = send(JsonObject().apply { addProperty("cmd", "tx_stop") })

    fun bridge(active: Boolean, a: Int, b: Int, c: Int? = null, aTg: Int = 23530, bTg: Int = aTg, cTg: Int = aTg) = send(JsonObject().apply {
        addProperty("cmd", "bridge_state"); addProperty("active", active); addProperty("a_node", a); addProperty("a_tg", aTg)
        addProperty("b_node", b); addProperty("b_tg", bTg); c?.let { addProperty("c_node", it); addProperty("c_tg", cTg) }
    })

    fun setVocoder(type: String) = send(JsonObject().apply { addProperty("cmd", "set_vocoder"); addProperty("type", type) })
    fun setDv30(count: Int, first: String, second: String) = send(JsonObject().apply {
        addProperty("cmd", "set_dv30"); addProperty("count", count); addProperty("addr1", first); addProperty("addr2", second)
    })

    fun repeaterId(): Long {
        val base = settings.dmrId.toLongOrNull() ?: 0
        val suffix = settings.essid.toIntOrNull()?.coerceIn(0, 99) ?: 0
        return base * 100 + suffix
    }
}
