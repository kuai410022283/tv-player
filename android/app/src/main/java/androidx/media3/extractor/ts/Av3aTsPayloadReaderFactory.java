package androidx.media3.extractor.ts;
import android.util.SparseArray;
import androidx.media3.extractor.ts.DefaultTsPayloadReaderFactory;
import androidx.media3.extractor.ts.PesReader;
import androidx.media3.extractor.ts.TsPayloadReader;

import java.util.List;

public class Av3aTsPayloadReaderFactory implements TsPayloadReader.Factory {

    private final DefaultTsPayloadReaderFactory defaultFactory;

    // AVS3 audio might use private stream types like 0x8A or 0x06.
    // We can list potential AV3A stream types or intercept unknown ones.
    private static final int STREAM_TYPE_AV3A_PRIVATE_1 = 0x8A;
    private static final int STREAM_TYPE_AV3A_PRIVATE_2 = 129; // sometimes AC3 is misused
    
    public Av3aTsPayloadReaderFactory(int flags, List<androidx.media3.common.Format> closedCaptionFormats) {
        // Initialize default factory to handle all standard streams
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
        // If stream is an unknown or private audio stream, we inject our AV3A reader.
        // It will sniff for 0xFFF sync word. If not AV3A, it will just drop frames.
        if (streamType == STREAM_TYPE_AV3A_PRIVATE_1 || streamType == STREAM_TYPE_AV3A_PRIVATE_2 || streamType == 0x06 || streamType == 0x81) {
            // For safety, we only return Av3aReader for specific stream types
            // that are known to sometimes encapsulate AV3A audio streams.
            // Returning Av3aReader wrapped in a PesReader to handle PES packet headers.
            return new PesReader(new Av3aReader(esInfo.language, esInfo.getRoleFlags()));
        }

        // Delegate all other standard streams (H264, AAC, AC3, HEVC, etc.) to ExoPlayer's default implementation
        return defaultFactory.createPayloadReader(streamType, esInfo);
    }
}
