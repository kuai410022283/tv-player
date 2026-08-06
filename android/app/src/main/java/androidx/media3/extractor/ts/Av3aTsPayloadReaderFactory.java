package androidx.media3.extractor.ts;
import android.util.SparseArray;
import androidx.media3.extractor.ts.DefaultTsPayloadReaderFactory;
import androidx.media3.extractor.ts.PesReader;
import androidx.media3.extractor.ts.TsPayloadReader;

import java.util.List;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Factory that intercepts private stream types potentially carrying AV3A audio and uses
 * {@link SmartAudioSnifferReader} to dynamically detect the true format (AC-3, AV3A, etc.).
 *
 * <p>Once a stream type is sniffed and found to be non-AV3A (e.g. AC-3), the result is cached
 * so that subsequent streams of the same type are handed directly to the default ExoPlayer
 * reader, bypassing sniffing entirely. This eliminates the per-switch sniffing overhead for
 * the vast majority of TS channels that use standard AC-3 audio.
 */
public class Av3aTsPayloadReaderFactory implements TsPayloadReader.Factory {

    private final DefaultTsPayloadReaderFactory defaultFactory;

    private static final int STREAM_TYPE_AV3A_PRIVATE_1 = 0x8A;
    private static final int STREAM_TYPE_AV3A_PRIVATE_2 = 129; // 0x81
    private static final int STREAM_TYPE_AV3A_STANDARD = 0xD5;
    private static final int STREAM_TYPE_AV3A_ALT1 = 0xD1;
    private static final int STREAM_TYPE_AV3A_ALT2 = 0xDA;

    public Av3aTsPayloadReaderFactory(int flags, List<androidx.media3.common.Format> closedCaptionFormats) {
        this.defaultFactory = new DefaultTsPayloadReaderFactory(flags, closedCaptionFormats);
    }

    public Av3aTsPayloadReaderFactory() {
        this(0, java.util.Collections.emptyList());
    }

    @Override
    public SparseArray<TsPayloadReader> createInitialPayloadReaders() {
        return defaultFactory.createInitialPayloadReaders();
    }

    @Override
    public TsPayloadReader createPayloadReader(int streamType, TsPayloadReader.EsInfo esInfo) {
        // 对于明确标准的 STREAM_TYPE_AV3A_STANDARD (0xD5)，无需嗅探，直接交由 Av3aReader 解析
        if (streamType == STREAM_TYPE_AV3A_STANDARD) {
            return new PesReader(new Av3aReader(esInfo.language, esInfo.getRoleFlags()));
        }

        // 对于私有流类型 (0x06, 0x81 等)，每次换台建立 Stream 时独立嗅探，避免静态 Cache 造成跨频道误判
        if (streamType == STREAM_TYPE_AV3A_PRIVATE_1 || streamType == STREAM_TYPE_AV3A_PRIVATE_2
                || streamType == STREAM_TYPE_AV3A_ALT1 || streamType == STREAM_TYPE_AV3A_ALT2
                || streamType == 0x06 || streamType == 0x81) {

            return new PesReader(new SmartAudioSnifferReader(
                    esInfo.language, esInfo.getRoleFlags()));
        }

        return defaultFactory.createPayloadReader(streamType, esInfo);
    }
}
