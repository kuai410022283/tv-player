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

    // Cache: streamType -> isAv3a (once sniffed).  Key present means "sniffed, result known".
    private static final ConcurrentHashMap<Integer, Boolean> sniffCache = new ConcurrentHashMap<>();

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
        if (streamType == STREAM_TYPE_AV3A_PRIVATE_1 || streamType == STREAM_TYPE_AV3A_PRIVATE_2
                || streamType == 0x06 || streamType == 0x81) {

            // If we already sniffed this stream type and it was NOT AV3A, skip sniffing
            Boolean cached = sniffCache.get(streamType);
            if (cached != null && !cached) {
                return defaultFactory.createPayloadReader(streamType, esInfo);
            }

            // If cached as AV3A, go straight to Av3aReader (no sniffing needed)
            if (cached != null) { // cached == true (AV3A)
                return new PesReader(new Av3aReader(esInfo.language, esInfo.getRoleFlags()));
            }

            // First time seeing this stream type — sniff and cache the result
            return new PesReader(new SmartAudioSnifferReader(
                    esInfo.language, esInfo.getRoleFlags(),
                    isAv3a -> sniffCache.put(streamType, isAv3a)));
        }

        return defaultFactory.createPayloadReader(streamType, esInfo);
    }
}
