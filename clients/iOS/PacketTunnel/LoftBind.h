#import <Mobile/Mobile.h>

/// Objective-C glue for the gomobile framework. The provider passes itself
/// to the engine as the packet sink through this C function.

/// gomobile emits both @protocol MobilePacketSink and @class MobilePacketSink.
/// Swift binds the class; this is the protocol the provider implements.
@protocol LoftPacketSink <MobilePacketSink>
@end

/// Makes the engine push packets to sink instead of queueing them for PullPacket.
void LoftEngineSetSink(MobileEngine *_Nonnull eng, id<MobilePacketSink> _Nullable sink);
