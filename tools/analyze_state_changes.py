#!/usr/bin/env python3
"""
Analyze state-changes.bin file for gaps in block heights.
This reads the binary state-changes files and checks for missing blocks.

Usage:
    python analyze_state_changes.py /path/to/state-changes-dir
    
Or set STATE_CHANGE_DIR environment variable:
    STATE_CHANGE_DIR=/opt/volumes/backend/state-changes python analyze_state_changes.py
"""
import os
import struct
import sys
from pathlib import Path
from collections import defaultdict

def read_uvarint(f):
    """Read a uvarint (variable-length unsigned integer) from file"""
    result = 0
    shift = 0
    while True:
        byte_data = f.read(1)
        if not byte_data:
            return None
        byte = byte_data[0]
        result |= (byte & 0x7F) << shift
        if byte & 0x80 == 0:
            break
        shift += 7
    return result

def read_uvarint_from_bytes(data, offset):
    """Read a uvarint starting at offset, return (value, new_offset)"""
    result = 0
    shift = 0
    pos = offset
    while pos < len(data):
        byte = data[pos]
        result |= (byte & 0x7F) << shift
        pos += 1
        if byte & 0x80 == 0:
            break
        shift += 7
    return result, pos

def parse_state_change_entry(entry_bytes):
    """
    Parse a StateChangeEntry from bytes.
    Format (all multi-byte values are varints):
        [operation type][is reverted][encoder type][key length][key]...
    
    Returns: (encoder_type, block_height) or (None, None) if not parseable
    """
    if len(entry_bytes) < 3:
        return None, None
    
    try:
        # Read operation type (varint)
        pos = 0
        operation_type, pos = read_uvarint_from_bytes(entry_bytes, pos)
        
        # Read is_reverted (single byte boolean)
        if pos >= len(entry_bytes):
            return None, None
        is_reverted = entry_bytes[pos] != 0
        pos += 1
        
        # Read encoder type (varint) 
        if pos >= len(entry_bytes):
            return None, None
        encoder_type, pos = read_uvarint_from_bytes(entry_bytes, pos)
        
        # For block entries (encoder_type == 2), try to find block height
        # The block height is stored later in the entry as a uint64
        # Try multiple offsets from the end
        block_height = None
        if encoder_type == 2 and len(entry_bytes) >= 24:
            # Block height is typically stored as an 8-byte uint64 near the end
            for offset_from_end in [8, 16, 24, 32, 40, 48]:
                if len(entry_bytes) >= offset_from_end:
                    try:
                        candidate = struct.unpack('<Q', entry_bytes[-offset_from_end:-offset_from_end+8])[0]
                        # Sanity check: reasonable block height (0 < height < 100M)
                        if 0 < candidate < 100000000:
                            block_height = candidate
                            break
                    except:
                        pass
        
        return encoder_type, block_height
        
    except Exception as e:
        return None, None

def analyze_state_changes(state_change_dir):
    """Main analysis function"""
    index_path = os.path.join(state_change_dir, "state-changes-index.bin")
    data_path = os.path.join(state_change_dir, "state-changes.bin")
    
    if not os.path.exists(index_path):
        print(f"❌ Error: Index file not found: {index_path}")
        return False
    
    if not os.path.exists(data_path):
        print(f"❌ Error: Data file not found: {data_path}")
        return False
    
    print(f"📂 Analyzing state-changes in: {state_change_dir}")
    print()
    
    # Get file sizes
    index_size = os.path.getsize(index_path)
    data_size = os.path.getsize(data_path)
    print(f"📊 Index file size: {index_size:,} bytes ({index_size / (1024**3):.2f} GB)")
    print(f"📊 Data file size: {data_size:,} bytes ({data_size / (1024**3):.2f} GB)")
    
    total_entries = index_size // 8
    print(f"📊 Total entries in index: {total_entries:,}")
    print()
    
    # Open files
    print("🔍 Scanning entries for blocks...")
    print("   (Showing first few entries for debugging...)")
    print()
    
    with open(index_path, 'rb') as index_file, open(data_path, 'rb') as data_file:
        block_heights = {}  # height -> entry_index
        encoder_types_count = defaultdict(int)
        max_height = 0
        min_height = float('inf')
        block_count = 0
        parse_errors = 0
        debug_count = 0
        
        for entry_idx in range(total_entries):
            if entry_idx % 100000 == 0 and entry_idx > 0:
                pct = (entry_idx / total_entries) * 100
                print(f"  Progress: {entry_idx:,}/{total_entries:,} ({pct:.1f}%) - {block_count:,} blocks found")
            
            try:
                # Read index entry (8 bytes, little endian)
                index_file.seek(entry_idx * 8)
                index_bytes = index_file.read(8)
                if len(index_bytes) != 8:
                    continue
                
                db_index = struct.unpack('<Q', index_bytes)[0]
                
                # Seek to data position
                data_file.seek(db_index)
                
                # Read entry length (uvarint)
                entry_length = read_uvarint(data_file)
                if entry_length is None or entry_length > 10 * 1024 * 1024:  # Sanity check: max 10MB per entry
                    continue
                
                # Read entry bytes
                entry_bytes = data_file.read(entry_length)
                if len(entry_bytes) != entry_length:
                    continue
                
                # Parse entry
                encoder_type, block_height = parse_state_change_entry(entry_bytes)
                
                if encoder_type is not None:
                    encoder_types_count[encoder_type] += 1
                
                # Debug: Show first few entries
                if debug_count < 50 and encoder_type is not None:
                    debug_count += 1
                    # Show raw bytes for debugging
                    raw_preview = entry_bytes[:20].hex() if len(entry_bytes) >= 20 else entry_bytes.hex()
                    print(f"  DEBUG Entry #{entry_idx}: encoder_type={encoder_type}, entry_len={len(entry_bytes)}, raw_start={raw_preview}")
                    if debug_count == 50:
                        print()
                        print("  ... continuing scan (no more debug output) ...")
                        print()
                
                # EncoderTypeBlock = 2 (but we're seeing type 0, so something is wrong)
                # For now, let's just count encoder types
                if encoder_type == 2 and block_height is not None:
                    # Sanity check: reasonable block height
                    if 0 < block_height < 100000000:
                        block_heights[block_height] = entry_idx
                        block_count += 1
                        
                        if block_height > max_height:
                            max_height = block_height
                        if block_height < min_height:
                            min_height = block_height
                            
            except Exception as e:
                parse_errors += 1
                if parse_errors < 10:  # Only show first few errors
                    print(f"  Warning: Parse error at entry {entry_idx}: {e}")
    
    print()
    print("=" * 70)
    print("📋 ANALYSIS RESULTS")
    print("=" * 70)
    print()
    
    print("📦 Entry Types Found:")
    encoder_type_names = {
        0: "Type_0 (UNKNOWN)",
        1: "Type_1",
        2: "Block",
        3: "Transaction", 
        4: "Post",
        5: "Profile",
        6: "Like",
        7: "Follow",
        8: "NFT",
        9: "NFTBid",
        10: "DerivedKey",
        # Add more as needed
    }
    for enc_type, count in sorted(encoder_types_count.items()):
        name = encoder_type_names.get(enc_type, f"Type_{enc_type}")
        pct = (count / total_entries) * 100 if total_entries > 0 else 0
        print(f"  {name}: {count:,} ({pct:.2f}%)")
    print()
    
    # Check if all entries are type 0 (parser issue)
    if encoder_types_count.get(0, 0) == total_entries:
        print()
        print("⚠️  CRITICAL: All entries showing encoder type 0!")
        print("   The binary format parser may be incorrect, or the file format")
        print("   may have changed. All entries showing type 0 is abnormal.")
        print()
        print("DIAGNOSIS:")
        print("  The state-changes binary format has likely changed or")
        print("  this parser is incompatible with your version.")
        print()
        print("RECOMMENDATION:")
        print("  Since you can't parse the state-changes file directly,")
        print("  use the REPAIR TOOL to fill gaps via the node API:")
        print("  ")
        print("    docker-compose -f repair-compose.yml up")
        print()
        print("  The repair tool fetches missing blocks via HTTP API,")
        print("  which is more reliable than parsing binary files.")
        print()
    print()
    
    if block_count == 0:
        print("❌ No blocks found in state-changes file!")
        return False
    
    print(f"✅ Total blocks found: {block_count:,}")
    print(f"📍 Min block height: {min_height:,}")
    print(f"📍 Max block height: {max_height:,}")
    
    expected = max_height - min_height + 1
    missing_count = expected - block_count
    
    print(f"📊 Expected blocks (continuous range): {expected:,}")
    print(f"🔴 Missing blocks: {missing_count:,}")
    
    if missing_count > 0:
        pct_missing = (missing_count / expected) * 100
        print(f"⚠️  Missing percentage: {pct_missing:.2f}%")
    
    print()
    print("=" * 70)
    print("🔍 GAP ANALYSIS")
    print("=" * 70)
    print()
    
    # Find gaps
    heights = sorted(block_heights.keys())
    
    gaps = []
    for i in range(len(heights) - 1):
        current = heights[i]
        next_height = heights[i + 1]
        if next_height != current + 1:
            # Found a gap
            gaps.append((current + 1, next_height - 1))
    
    if not gaps:
        print("✅ No gaps found! State-changes file has complete sequential blocks.")
    else:
        print(f"⚠️  Found {len(gaps)} gap(s):")
        print()
        
        total_missing = 0
        for i, (start, end) in enumerate(gaps[:50], 1):  # Show first 50 gaps
            missing = end - start + 1
            total_missing += missing
            print(f"  Gap #{i:3d}: heights {start:>10,} → {end:>10,} ({missing:>8,} blocks missing)")
        
        if len(gaps) > 50:
            remaining_missing = sum(end - start + 1 for start, end in gaps[50:])
            print(f"  ... and {len(gaps) - 50} more gaps ({remaining_missing:,} blocks)")
            total_missing += remaining_missing
        
        print()
        print(f"  Total missing from gaps: {total_missing:,} blocks")
    
    print()
    print("=" * 70)
    print("📌 RECENT BLOCKS (Last 20)")
    print("=" * 70)
    print()
    
    for height in heights[-20:]:
        entry_idx = block_heights[height]
        print(f"  Height {height:>10,} (entry index: {entry_idx:>12,})")
    
    print()
    print("=" * 70)
    print("✨ Analysis complete!")
    print("=" * 70)
    
    return True

if __name__ == "__main__":
    # Get state change directory from args or environment
    if len(sys.argv) > 1:
        state_change_dir = sys.argv[1]
    else:
        state_change_dir = os.environ.get('STATE_CHANGE_DIR', '/tmp/state-changes')
    
    print()
    print("=" * 70)
    print("🔬 DeSo State-Changes Gap Analyzer")
    print("=" * 70)
    print()
    
    success = analyze_state_changes(state_change_dir)
    sys.exit(0 if success else 1)
