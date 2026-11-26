import Foundation
import Metal
import CoreVideo
import CoreImage
import AppKit

func makeRenderer() throws -> any Renderer { try MeshRenderer() }
enum RenderFailure: Error { case invalidInput, unavailable, closed, gpuFailure }

// No private MyGo fields, CPU readback in the frame path, or permanent timer.
final class MeshRenderer: Renderer {
    private let lock = NSLock()
    private let device: any MTLDevice
    private let queue: any MTLCommandQueue
    private let pipeline: any MTLRenderPipelineState
    private let pixelPipeline: any MTLRenderPipelineState
    private let depthState: any MTLDepthStencilState
    private var output: NativeTexture?
    private var pool: CVPixelBufferPool?
    private var vertices: (any MTLBuffer)?
    private var depth: (any MTLTexture)?
    private var latest: CVPixelBuffer?
    private var count = 0, joints = 0, size = 0
    private var closed = false
    private var pixelVertices: (any MTLBuffer)?
    private var morphs: (any MTLBuffer)?
    private var indices: (any MTLBuffer)?
    private var poseBuffer: (any MTLBuffer)?
    private var pixelCount = 0, indexCount = 0
    private var drawCalls: Int64 = 0

    init() throws {
        guard let device = MTLCreateSystemDefaultDevice(), let queue = device.makeCommandQueue() else { throw RenderFailure.unavailable }
        self.device = device; self.queue = queue
        let shader = """
        #include <metal_stdlib>
        using namespace metal;
        struct Out { float4 position [[position]]; float3 normal; float3 color; };
        vertex Out companion_vertex(uint id [[vertex_id]], const device float *vertices [[buffer(0)]], const device float4x4 *matrices [[buffer(1)]]) {
            uint b = id * 10;
            float3 p = float3(vertices[b], vertices[b+1], vertices[b+2]);
            float3 n = float3(vertices[b+3], vertices[b+4], vertices[b+5]);
            float4x4 world = matrices[uint(vertices[b+9])+1];
            Out o; o.position = matrices[0]*world*float4(p,1);
            o.normal = normalize((world*float4(n,0)).xyz);
            o.color = float3(vertices[b+6],vertices[b+7],vertices[b+8]); return o;
        }
        fragment float4 companion_fragment(Out o [[stage_in]]) {
            float light = max(0.0,dot(normalize(o.normal),normalize(float3(-0.5,0.8,1.0))));
            return float4(pow(clamp(o.color*(0.57+0.43*light),0.0,1.0),float3(1.0/2.2)),1.0);
        }
        struct PixelOut { float4 position [[position]]; float3 world; float4 color; };
        vertex PixelOut pixel_vertex(uint id [[vertex_id]], const device float *v [[buffer(0)]],
            const device float4x4 *m [[buffer(1)]], const device float *targets [[buffer(2)]], constant uint4 &config [[buffer(3)]]) {
            uint b=id*16; float3 p=float3(v[b],v[b+1],v[b+2]);
            if(config.y>0){uint t=id*24+(config.y-1)*3;p+=float3(targets[t],targets[t+1],targets[t+2]);}
            float4 world=float4(0);
            for(uint k=0;k<4;k++){float w=v[b+12+k];if(w>0)world+=w*(m[uint(v[b+8+k])+1]*float4(p,1));}
            PixelOut o;o.position=m[0]*world;o.world=world.xyz;o.color=float4(v[b+4],v[b+5],v[b+6],v[b+7]);return o;
        }
        fragment float4 pixel_fragment(PixelOut o [[stage_in]], constant uint4 &config [[buffer(0)]]) {
            float3 color=o.color.rgb;
            if(config.z!=0){float3 n=normalize(cross(dfdx(o.world),dfdy(o.world)));float light=abs(dot(n,normalize(float3(-.5,.8,1))));color*=.75+.25*light;}
            float3 srgb=select(1.055*pow(max(color,float3(0)),float3(1.0/2.4))-.055,12.92*color,color<=.0031308);
            return float4(clamp(srgb,0.0,1.0)*o.color.a,o.color.a);
        }
        """
        let library = try device.makeLibrary(source:shader,options:nil)
        let descriptor = MTLRenderPipelineDescriptor()
        descriptor.vertexFunction = library.makeFunction(name:"companion_vertex")
        descriptor.fragmentFunction = library.makeFunction(name:"companion_fragment")
        descriptor.colorAttachments[0].pixelFormat = .bgra8Unorm
        descriptor.depthAttachmentPixelFormat = .depth32Float
        pipeline = try device.makeRenderPipelineState(descriptor:descriptor)
        descriptor.vertexFunction = library.makeFunction(name:"pixel_vertex")
        descriptor.fragmentFunction = library.makeFunction(name:"pixel_fragment")
        pixelPipeline = try device.makeRenderPipelineState(descriptor:descriptor)
        let d = MTLDepthStencilDescriptor(); d.depthCompareFunction = .less; d.isDepthWriteEnabled = true
        guard let state = device.makeDepthStencilState(descriptor:d) else { throw RenderFailure.unavailable }
        depthState = state
    }

    private func validFloats(_ data: Data, vertexJoints: Int? = nil) -> Bool {
        data.withUnsafeBytes { raw in
            for i in 0..<(data.count/4) {
                let v = Float(bitPattern:UInt32(littleEndian:raw.loadUnaligned(fromByteOffset:i*4,as:UInt32.self)))
                if !v.isFinite { return false }
                if let limit = vertexJoints, i%10 == 9, (v<0 || v>=Float(limit) || v.rounded() != v) { return false }
            }; return true
        }
    }

    func initialize(texture:NativeTexture,vertices:Data,joints:Int,size:Int) throws {
        lock.lock(); defer { lock.unlock() }
        guard !closed else { throw RenderFailure.closed }
        guard joints>0 && joints<=256, size>=64 && size<=1024, !vertices.isEmpty,
              vertices.count<=4*1024*1024, vertices.count%120==0, validFloats(vertices,vertexJoints:joints) else { throw RenderFailure.invalidInput }
        var attrs=texture.pixelBufferAttributes
        attrs[kCVPixelBufferWidthKey as String]=size; attrs[kCVPixelBufferHeightKey as String]=size
        var newPool:CVPixelBufferPool?
        guard CVPixelBufferPoolCreate(nil,nil,attrs as CFDictionary,&newPool)==kCVReturnSuccess else { throw RenderFailure.unavailable }
        let buffer=vertices.withUnsafeBytes { device.makeBuffer(bytes:$0.baseAddress!,length:vertices.count,options:.storageModeShared) }
        let descriptor=MTLTextureDescriptor.texture2DDescriptor(pixelFormat:.depth32Float,width:size,height:size,mipmapped:false)
        descriptor.usage = .renderTarget; descriptor.storageMode = .private
        guard let buffer, let newDepth=device.makeTexture(descriptor:descriptor) else { throw RenderFailure.unavailable }
        self.pixelVertices=nil; self.morphs=nil; self.indices=nil; self.poseBuffer=nil
        self.vertices=buffer; depth=newDepth; pool=newPool; count=vertices.count/40
        self.joints=joints; self.size=size; output=texture; latest=nil
    }

    func draw(matrices:Data) throws {
        lock.lock(); defer { lock.unlock() }
        guard !closed else { throw RenderFailure.closed }
        guard let output, let pool, let vertices, let depth, matrices.count==(joints+1)*64, validFloats(matrices) else { throw RenderFailure.invalidInput }
        var next:CVPixelBuffer?
        let status=CVPixelBufferPoolCreatePixelBufferWithAuxAttributes(nil,pool,[kCVPixelBufferPoolAllocationThresholdKey as String:6] as CFDictionary,&next)
        if status==kCVReturnWouldExceedAllocationThreshold { return }
        guard status==kCVReturnSuccess, let next, let surface=CVPixelBufferGetIOSurface(next)?.takeUnretainedValue() else { throw RenderFailure.unavailable }
        let descriptor=MTLTextureDescriptor.texture2DDescriptor(pixelFormat:.bgra8Unorm,width:size,height:size,mipmapped:false)
        descriptor.usage=[.renderTarget,.shaderRead]; descriptor.storageMode = .shared
        guard let target=device.makeTexture(descriptor:descriptor,iosurface:surface,plane:0), let command=queue.makeCommandBuffer() else { throw RenderFailure.unavailable }
        let pass=MTLRenderPassDescriptor()
        pass.colorAttachments[0].texture=target; pass.colorAttachments[0].loadAction = .clear; pass.colorAttachments[0].storeAction = .store
        pass.colorAttachments[0].clearColor=MTLClearColorMake(0,0,0,0)
        pass.depthAttachment.texture=depth; pass.depthAttachment.loadAction = .clear; pass.depthAttachment.storeAction = .dontCare; pass.depthAttachment.clearDepth=1
        guard let encoder=command.makeRenderCommandEncoder(descriptor:pass) else { throw RenderFailure.unavailable }
        let transforms=matrices.withUnsafeBytes { device.makeBuffer(bytes:$0.baseAddress!,length:matrices.count,options:.storageModeShared) }
        guard let transforms else { encoder.endEncoding(); throw RenderFailure.unavailable }
        encoder.setRenderPipelineState(pipeline); encoder.setDepthStencilState(depthState); encoder.setCullMode(.none)
        encoder.setVertexBuffer(vertices,offset:0,index:0); encoder.setVertexBuffer(transforms,offset:0,index:1)
        encoder.drawPrimitives(type:.triangle,vertexStart:0,vertexCount:count); encoder.endEncoding()
        command.commit(); command.waitUntilCompleted()
        guard command.status == .completed else { throw RenderFailure.gpuFailure }
        latest=next; output.present(next); drawCalls += 1
    }

    private func validRawFloats(_ raw: UnsafeMutableRawBufferPointer) -> Bool {
        for i in 0..<(raw.count/4) {
            let value=Float(bitPattern:UInt32(littleEndian:raw.loadUnaligned(fromByteOffset:i*4,as:UInt32.self)))
            if !value.isFinite { return false }
        }
        return true
    }

    func initializePixel(texture: NativeTexture, vertices: NativeBuffer, morphs: NativeBuffer, indices: NativeBuffer, joints: Int, size: Int) throws {
        lock.lock(); defer { lock.unlock() }
        guard !closed else { throw RenderFailure.closed }
        let vertexCount=vertices.count/64
        guard vertexCount>0, vertexCount<=200000, vertices.count%64==0,
              morphs.count==vertexCount*96, indices.count>0, indices.count<=450000*4, indices.count%12==0,
              joints>0, joints<=64, size>=64, size<=1024, validRawFloats(vertices.bytes), validRawFloats(morphs.bytes) else { throw RenderFailure.invalidInput }
        let attributesValid: Bool = { let raw=vertices.bytes
            for i in 0..<vertexCount {
                var total: Float=0
                for k in 0..<4 {
                    let joint=Float(bitPattern:UInt32(littleEndian:raw.loadUnaligned(fromByteOffset:(i*16+8+k)*4,as:UInt32.self)))
                    let weight=Float(bitPattern:UInt32(littleEndian:raw.loadUnaligned(fromByteOffset:(i*16+12+k)*4,as:UInt32.self)))
                    if joint<0 || joint>=Float(joints) || joint.rounded() != joint || weight<0 || weight>1 { return false }
                    total += weight
                }
                if abs(total-1)>0.00001 { return false }
            }
            return true
        }()
        let indicesValid: Bool = { let raw=indices.bytes
            for i in 0..<(indices.count/4) { if UInt32(littleEndian:raw.loadUnaligned(fromByteOffset:i*4,as:UInt32.self))>=vertexCount { return false } }
            return true
        }()
        guard attributesValid, indicesValid else { throw RenderFailure.invalidInput }
        let vb=device.makeBuffer(bytes:vertices.bytes.baseAddress!,length:vertices.count,options:.storageModeShared)
        let mb=device.makeBuffer(bytes:morphs.bytes.baseAddress!,length:morphs.count,options:.storageModeShared)
        let ib=device.makeBuffer(bytes:indices.bytes.baseAddress!,length:indices.count,options:.storageModeShared)
        let pb=device.makeBuffer(length:(joints+1)*64,options:.storageModeShared)
        var attrs=texture.pixelBufferAttributes
        attrs[kCVPixelBufferWidthKey as String]=size;attrs[kCVPixelBufferHeightKey as String]=size
        var nextPool:CVPixelBufferPool?
        let descriptor=MTLTextureDescriptor.texture2DDescriptor(pixelFormat:.depth32Float,width:size,height:size,mipmapped:false)
        descriptor.usage = .renderTarget;descriptor.storageMode = .private
        guard CVPixelBufferPoolCreate(nil,nil,attrs as CFDictionary,&nextPool)==kCVReturnSuccess,
              let vb, let mb, let ib, let pb, let nextDepth=device.makeTexture(descriptor:descriptor) else {throw RenderFailure.unavailable}
        self.vertices=nil;self.pixelVertices=vb;self.morphs=mb;self.indices=ib;self.poseBuffer=pb
        self.pool=nextPool;self.depth=nextDepth;self.output=texture;self.latest=nil
        self.pixelCount=vertexCount;self.indexCount=indices.count/4;self.joints=joints;self.size=size
    }

    func drawPixel(matrices: Data, pose: Int, lighting: Bool) throws {
        lock.lock(); defer { lock.unlock() }
        guard !closed else { throw RenderFailure.closed }
        guard let output, let pool, let depth, let vertices=pixelVertices, let morphs, let indices, let poseBuffer,
              matrices.count==(joints+1)*64, pose>=0, pose<=8, validFloats(matrices) else { throw RenderFailure.invalidInput }
        matrices.withUnsafeBytes { raw in poseBuffer.contents().copyMemory(from:raw.baseAddress!,byteCount:matrices.count) }
        var next:CVPixelBuffer?
        let status=CVPixelBufferPoolCreatePixelBufferWithAuxAttributes(nil,pool,[kCVPixelBufferPoolAllocationThresholdKey as String:6] as CFDictionary,&next)
        if status==kCVReturnWouldExceedAllocationThreshold { return }
        guard status==kCVReturnSuccess, let next, let surface=CVPixelBufferGetIOSurface(next)?.takeUnretainedValue() else {throw RenderFailure.unavailable}
        let descriptor=MTLTextureDescriptor.texture2DDescriptor(pixelFormat:.bgra8Unorm,width:size,height:size,mipmapped:false)
        descriptor.usage=[.renderTarget,.shaderRead];descriptor.storageMode = .shared
        guard let target=device.makeTexture(descriptor:descriptor,iosurface:surface,plane:0), let command=queue.makeCommandBuffer() else {throw RenderFailure.unavailable}
        let pass=MTLRenderPassDescriptor()
        pass.colorAttachments[0].texture=target;pass.colorAttachments[0].loadAction = .clear;pass.colorAttachments[0].storeAction = .store
        pass.colorAttachments[0].clearColor=MTLClearColorMake(0,0,0,0)
        pass.depthAttachment.texture=depth;pass.depthAttachment.loadAction = .clear;pass.depthAttachment.storeAction = .dontCare;pass.depthAttachment.clearDepth=1
        guard let encoder=command.makeRenderCommandEncoder(descriptor:pass) else {throw RenderFailure.unavailable}
        var config=SIMD4<UInt32>(UInt32(pixelCount),UInt32(pose),lighting ? 1:0,0)
        encoder.setRenderPipelineState(pixelPipeline);encoder.setDepthStencilState(depthState)
        encoder.setCullMode(.back);encoder.setFrontFacing(.counterClockwise)
        encoder.setVertexBuffer(vertices,offset:0,index:0);encoder.setVertexBuffer(poseBuffer,offset:0,index:1)
        encoder.setVertexBuffer(morphs,offset:0,index:2);encoder.setVertexBytes(&config,length:16,index:3)
        encoder.setFragmentBytes(&config,length:16,index:0)
        encoder.drawIndexedPrimitives(type:.triangle,indexCount:indexCount,indexType:.uint32,indexBuffer:indices,indexBufferOffset:0)
        encoder.endEncoding();command.commit();command.waitUntilCompleted()
        guard command.status == .completed else {throw RenderFailure.gpuFailure}
        latest=next;output.present(next);drawCalls += 1
    }

    func stats() throws -> RenderStats {
        lock.lock();defer {lock.unlock()}
        guard !closed else {throw RenderFailure.closed}
        return RenderStats(vertexBytes:Int64(pixelVertices?.length ?? vertices?.length ?? 0),morphBytes:Int64(morphs?.length ?? 0),indexBytes:Int64(indices?.length ?? 0),poseBytes:Int64(poseBuffer?.length ?? 0),drawCalls:drawCalls)
    }
    private func releaseBuffers() {
        latest=nil;vertices=nil;pixelVertices=nil;morphs=nil;indices=nil;poseBuffer=nil;depth=nil;pool=nil;output=nil
        count=0;pixelCount=0;indexCount=0;joints=0;size=0
    }
    func unload() throws {
        lock.lock();defer {lock.unlock()}
        guard !closed else {throw RenderFailure.closed}
        releaseBuffers()
    }

    func snapshot() throws -> Data {
        lock.lock(); defer { lock.unlock() }
        guard !closed, let latest else { throw RenderFailure.closed }
        let context=CIContext(mtlDevice:device)
        guard let image=context.createCGImage(CIImage(cvPixelBuffer:latest),from:CGRect(x:0,y:0,width:size,height:size)),
              let png=NSBitmapImageRep(cgImage:image).representation(using:.png,properties:[:]) else { throw RenderFailure.gpuFailure }
        return png
    }

    func close() {
        lock.lock(); defer { lock.unlock() }
        closed=true; releaseBuffers()
    }
}
