package uk.co.dvhub.android

import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.math.abs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PcmAudioTest {
    @Test fun downsampleProducesOneGatewayFrame() {
        val source = ShortArray(PcmAudio.DEVICE_SAMPLES_PER_FRAME) { 1200 }
        val result = PcmAudio.downsample48kTo8k(source)
        assertEquals(PcmAudio.GATEWAY_BYTES_PER_FRAME, result.size)
        assertEquals(1200, ByteBuffer.wrap(result).order(ByteOrder.LITTLE_ENDIAN).short.toInt())
    }

    @Test fun upsampleProducesTwentyMillisecondsAt48k() {
        val input = ByteBuffer.allocate(PcmAudio.GATEWAY_BYTES_PER_FRAME).order(ByteOrder.LITTLE_ENDIAN)
        repeat(PcmAudio.GATEWAY_SAMPLES_PER_FRAME) { input.putShort((it * 100).toShort()) }
        val result = PcmAudio.upsample8kTo48k(input.array())
        assertEquals(PcmAudio.DEVICE_SAMPLES_PER_FRAME, result.size)
        assertEquals(0, result.first().toInt())
        assertTrue(abs(result[6].toInt() - 100) <= 1)
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsPartialGatewayFrames() {
        PcmAudio.upsample8kTo48k(ByteArray(10))
    }
}
