# Regenerates testdata/ golden vectors from reference implementations.
# Not part of any dvxcode binary. CI runs `make golden` before tests.
#   make golden MBELIB=../../ref/mbelib DSD=../../ref/dsd MMDVM=../../ref/MMDVMHost
MBELIB ?= ../../ref/mbelib
DSD    ?= ../../ref/dsd
MMDVM  ?= ../../ref/MMDVMHost
SRCS = mbelib ecc ambe3600x2400 ambe3600x2450 imbe7200x4400 imbe7100x4400
MMSRC = BPTC19696 Hamming RS129 CRC DMRFullLC DMRLC DMREmbeddedData DMREMB DMRSlotType QR1676 Golay2087

refdump: refdump.c
	$(CC) -O2 -w -o $@ refdump.c $(addprefix $(MBELIB)/,$(addsuffix .c,$(SRCS))) -I$(MBELIB) -I$(DSD)/include -lm

dmr_ref: dmr_ref.cpp
	rm -rf mm && mkdir mm && cp $(MMDVM)/*.h $(addprefix $(MMDVM)/,$(addsuffix .cpp,$(MMSRC))) mm/ && cp mmdvm_Log.h mm/Log.h
	$(CXX) -O1 -w -DUSE_DMR -DUSE_DSTAR -Imm -o $@ dmr_ref.cpp mmdvm_Utils.cpp $(addprefix mm/,$(addsuffix .cpp,$(MMSRC)))

golden: refdump dmr_ref
	./refdump golay       > ../testdata/mbelib_golaymatrix.txt
	./refdump fec 5000    > ../testdata/mbelib_fec.txt
	./refdump parms dmr 3000   > ../testdata/mbelib_parms_dmr.txt
	./refdump parms dstar 3000 > ../testdata/mbelib_parms_dstar.txt
	./dmr_ref > ../testdata/dmr_golden.txt

clean:
	rm -rf refdump dmr_ref mm
