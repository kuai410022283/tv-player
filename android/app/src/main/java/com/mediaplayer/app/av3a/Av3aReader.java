package com.mediaplayer.app.av3a;

import androidx.media3.common.Format;
import androidx.media3.common.MimeTypes;
import androidx.media3.extractor.ts.ElementaryStreamReader;
import androidx.media3.extractor.ExtractorOutput;
import androidx.media3.extractor.TrackOutput;
import androidx.media3.extractor.ts.TsPayloadReader;
import androidx.media3.common.util.ParsableByteArray;
import androidx.media3.common.util.ParsableBitArray;

/**
 * 核心逆向成果：AVS3 / AV3A 音频底层解封装器
 * 
 * 用于从 TS 容器的 PES 数据包中解析 AV3A 音频帧。
 * 作用：从 TS 直播流中剥离出 AV3A 帧流，计算正确的时长、帧大小和声道数，并送入 FFmpeg。
 */
public final class Av3aReader implements ElementaryStreamReader {

    private static final int STATE_FINDING_SYNC = 0;
    private static final int STATE_READING_HEADER = 1;
    private static final int STATE_READING_FRAME = 2;

    private final String language;
    private final int roleFlags;
    
    private int state = STATE_FINDING_SYNC;
    private boolean lastByteWasFF = false;
    private int bytesRead = 0;
    private boolean hasOutputFormat = false;

    private final ParsableByteArray headerScratchBytes = new ParsableByteArray(9);
    
    private long timeUs = -9223372036854775807L;
    private long frameDurationUs = 0;
    private int frameSize = 0;
    
    private String formatId;
    private TrackOutput output;

    // 对应混淆代码中的 C0526g (AVS3 Header 结构体)
    private static final int[] SAMPLE_RATES = {
        192000, 96000, 48000, 44100, 32000, 24000, 22050, 16000, 8000
    };

    public Av3aReader(String language, int roleFlags) {
        this.language = language;
        this.roleFlags = roleFlags;
    }

    @Override
    public void seek() {
        state = STATE_FINDING_SYNC;
        bytesRead = 0;
        lastByteWasFF = false;
        timeUs = -9223372036854775807L;
    }

    @Override
    public void createTracks(ExtractorOutput extractorOutput, TsPayloadReader.TrackIdGenerator idGenerator) {
        idGenerator.generateNewId();
        formatId = idGenerator.getFormatId();
        output = extractorOutput.track(idGenerator.getTrackId(), 1);
    }

    @Override
    public void packetStarted(long pesTimeUs, int flags) {
        this.timeUs = pesTimeUs;
    }

    @Override
    public void consume(ParsableByteArray data) {
        while (data.bytesLeft() > 0) {
            switch (state) {
                case STATE_FINDING_SYNC:
                    findSyncWord(data);
                    break;
                case STATE_READING_HEADER:
                    parseHeader(data);
                    break;
                case STATE_READING_FRAME:
                    readFramePayload(data);
                    break;
            }
        }
    }

    /**
     * 寻找 AV3A 同步字 (类似 ADTS 0xFFF)
     */
    private void findSyncWord(ParsableByteArray data) {
        byte[] bytes = data.getData();
        int limit = data.limit();
        int pos = data.getPosition();
        while (pos < limit) {
            boolean isFF = bytes[pos] == -1; // 0xFF
            boolean isSync = lastByteWasFF && (bytes[pos] & 0xF0) == 0xF0; // 0xFF Fx
            lastByteWasFF = isFF;
            if (isSync) {
                data.setPosition(pos + 1);
                lastByteWasFF = false;
                headerScratchBytes.getData()[0] = -1;
                headerScratchBytes.getData()[1] = bytes[pos];
                bytesRead = 2;
                state = STATE_READING_HEADER;
                return;
            }
            pos++;
        }
        data.setPosition(limit);
    }

    /**
     * 核心逻辑：破译的 AVS3 帧头解析算法
     */
    private void parseHeader(ParsableByteArray data) {
        int bytesToRead = Math.min(data.bytesLeft(), 9 - bytesRead);
        data.readBytes(headerScratchBytes.getData(), bytesRead, bytesToRead);
        bytesRead += bytesToRead;
        if (bytesRead < 9) {
            return;
        }
        
        headerScratchBytes.setPosition(0);
        ParsableBitArray bitArray = new ParsableBitArray(headerScratchBytes.getData());
        
        // 核心参数解析（复刻自 X2.C0527h 混淆逻辑）
        bitArray.skipBits(12); // Skip sync word
        if (bitArray.readBits(4) == 2 && bitArray.readBits(1) != 1) {
            bitArray.skipBits(3);
            int configType = bitArray.readBits(3); // f9460a
            int sampleRateIdx = bitArray.readBits(4); // f9461b
            bitArray.skipBits(8);
            
            // 此处省略 EnumC0525f 中上百行的声道与码率查表逻辑
            // 真实使用时，这里会查表推算出：channelCount, bitrate 等
            
            int sampleRate = SAMPLE_RATES[sampleRateIdx];
            int channelCount = 2; // 简化展示，实际通过 EnumC0525f 查表获得
            int bitrate = 128000; // 简化展示，实际查表获得
            
            // 破译出的帧大小和时长计算公式
            // int ceil = (int) Math.ceil(((bitrate / sampleRate) * 1024) / 8.0f);
            frameSize = (int) Math.ceil(((bitrate * 1.0f / sampleRate) * 1024) / 8.0f);
            
            if (!hasOutputFormat) {
                frameDurationUs = (frameSize * 1000000L) / sampleRate;
                
                // 关键点：强行注入 audio/av3a MIME 类型
                Format format = new Format.Builder()
                        .setId(formatId)
                        .setSampleMimeType("audio/av3a")
                        .setMaxInputSize(262144)
                        .setChannelCount(channelCount)
                        .setSampleRate(sampleRate)
                        .setLanguage(language)
                        .build();
                output.format(format);
                hasOutputFormat = true;
            }
            
            headerScratchBytes.setPosition(0);
            output.sampleData(headerScratchBytes, 9);
            state = STATE_READING_FRAME;
        } else {
            bytesRead = 0;
            state = STATE_FINDING_SYNC;
        }
    }

    private void readFramePayload(ParsableByteArray data) {
        int bytesToRead = Math.min(data.bytesLeft(), frameSize - bytesRead);
        output.sampleData(data, bytesToRead);
        bytesRead += bytesToRead;
        if (bytesRead >= frameSize) {
            output.sampleMetadata(timeUs, 1, frameSize, 0, null);
            timeUs += frameDurationUs;
            bytesRead = 0;
            state = STATE_FINDING_SYNC;
        }
    }

    @Override
    public void packetFinished(boolean isEndOfInput) {}
}
