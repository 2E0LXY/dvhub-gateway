package uk.co.dvhub.android

import android.Manifest
import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import com.google.gson.Gson
import com.google.gson.JsonObject
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import okio.ByteString
import okio.ByteString.Companion.toByteString
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import androidx.annotation.RequiresPermission

class VoiceClient(private val listener: Listener) {
    interface Listener { fun onVoiceState(state: String, message: String) }
    private val gson = Gson()
    private val client = OkHttpClient.Builder().connectTimeout(10, TimeUnit.SECONDS).readTimeout(15, TimeUnit.SECONDS).pingInterval(15, TimeUnit.SECONDS).build()
    private var settings = GatewaySettings()
    private var socket: WebSocket? = null
    private var recorder: AudioRecord? = null
    private var player: AudioTrack? = null
    private val transmitting = AtomicBoolean(false)
    private var token = ""

    fun configure(value: GatewaySettings) { settings = value.copy(serverUrl = value.serverUrl.trimEnd('/')) }

    fun connect() {
        close()
        val body = JsonObject().apply { addProperty("callsign", settings.callsign); addProperty("dmr_id", settings.dmrId.toLongOrNull() ?: 0); addProperty("password", settings.password) }
        val request = Request.Builder().url(settings.serverUrl + "/api/client/login")
            .post(gson.toJson(body).toRequestBody("application/json".toMediaType())).build()
        client.newCall(request).enqueue(object: Callback {
            override fun onFailure(call: Call, e: java.io.IOException) = listener.onVoiceState("fault", e.message ?: "Voice login failed")
            override fun onResponse(call: Call, response: Response) { response.use {
                val text = it.body.string(); if (!it.isSuccessful) { listener.onVoiceState("fault", "Voice login rejected (${it.code})"); return }
                token = gson.fromJson(text, JsonObject::class.java).get("token")?.asString.orEmpty(); requestTicket()
            } }
        })
    }

    private fun requestTicket() {
        val request = Request.Builder().url(settings.serverUrl + "/api/client/voice_ticket").header("Authorization", "Bearer $token")
            .post("{}".toRequestBody("application/json".toMediaType())).build()
        client.newCall(request).enqueue(object: Callback {
            override fun onFailure(call: Call, e: java.io.IOException) = listener.onVoiceState("fault", e.message ?: "Voice ticket failed")
            override fun onResponse(call: Call, response: Response) { response.use {
                val text=it.body.string(); if(!it.isSuccessful){listener.onVoiceState("fault","Voice ticket rejected (${it.code})");return}
                val ticket=gson.fromJson(text,JsonObject::class.java).get("ticket").asString; openSocket(ticket)
            } }
        })
    }

    private fun openSocket(ticket: String) {
        val url=settings.serverUrl.replaceFirst("https://","wss://").replaceFirst("http://","ws://")+"/voice?ticket=$ticket"
        socket=client.newWebSocket(Request.Builder().url(url).build(),object:WebSocketListener(){
            override fun onOpen(webSocket:WebSocket,response:Response){
                runCatching { preparePlayer() }
                    .onSuccess { listener.onVoiceState("connected","Voice connected — listening") }
                    .onFailure { listener.onVoiceState("fault", "This device could not start 8 kHz voice audio: ${it.message ?: it.javaClass.simpleName}") }
            }
            override fun onMessage(webSocket:WebSocket,text:String){runCatching{gson.fromJson(text,JsonObject::class.java)}.onSuccess{val state=it.get("state")?.asString?:"connected";listener.onVoiceState(state,it.get("reason")?.asString.orEmpty())}}
            override fun onMessage(webSocket:WebSocket,bytes:ByteString){
                runCatching { player?.write(bytes.toByteArray(),0,bytes.size) }
                    .onFailure { listener.onVoiceState("fault", "Audio playback failed: ${it.message ?: it.javaClass.simpleName}") }
            }
            override fun onFailure(webSocket:WebSocket,t:Throwable,response:Response?){listener.onVoiceState("fault",t.message?:"Voice disconnected")}
        })
    }

    private fun preparePlayer(){ val min=AudioTrack.getMinBufferSize(8000,AudioFormat.CHANNEL_OUT_MONO,AudioFormat.ENCODING_PCM_16BIT).coerceAtLeast(3200);player=AudioTrack.Builder().setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build()).setAudioFormat(AudioFormat.Builder().setSampleRate(8000).setEncoding(AudioFormat.ENCODING_PCM_16BIT).setChannelMask(AudioFormat.CHANNEL_OUT_MONO).build()).setBufferSizeInBytes(min).setTransferMode(AudioTrack.MODE_STREAM).build().apply{play()} }

    @RequiresPermission(Manifest.permission.RECORD_AUDIO)
    fun startTransmit(): Boolean {
        if(socket==null||transmitting.getAndSet(true))return false
        val started = runCatching {
            val min=AudioRecord.getMinBufferSize(8000,AudioFormat.CHANNEL_IN_MONO,AudioFormat.ENCODING_PCM_16BIT).coerceAtLeast(3200)
            recorder=AudioRecord(MediaRecorder.AudioSource.VOICE_COMMUNICATION,8000,AudioFormat.CHANNEL_IN_MONO,AudioFormat.ENCODING_PCM_16BIT,min).apply{startRecording()}
        }
        if (started.isFailure) {
            transmitting.set(false)
            runCatching { recorder?.release() }; recorder = null
            val error = started.exceptionOrNull()
            listener.onVoiceState("fault", "Microphone start failed: ${error?.message ?: error?.javaClass?.simpleName ?: "unknown error"}")
            return false
        }
        socket?.send("{\"type\":\"ptt_start\"}")
        Thread({val frame=ByteArray(320);while(transmitting.get()){var read=0;while(read<frame.size&&transmitting.get()){val count=recorder?.read(frame,read,frame.size-read)?:-1;if(count>0)read+=count else break};if(read==frame.size)socket?.send(frame.toByteString())}},"dvhub-audio-tx").start();return true
    }

    fun stopTransmit(){if(!transmitting.getAndSet(false))return;runCatching{recorder?.stop()};recorder?.release();recorder=null;socket?.send("{\"type\":\"ptt_stop\"}")}
    fun close(){stopTransmit();socket?.close(1000,"App stopped");socket=null;player?.stop();player?.release();player=null;token=""}
}
